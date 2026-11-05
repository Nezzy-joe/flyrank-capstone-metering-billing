# Build Log

Honest record of how this was built — including where AI helped, where it was wrong, and what I changed. Honesty is graded; perfection is not.

## 2026-11-02 — Phase 1: Design

**What I did:** Designed the full architecture and wrote the one-page design doc ([DESIGN.md](DESIGN.md)).

**Decisions I made (and why):**
- **Stack: Go** (not the brief's suggested Node/Python). Reason: Go is my strongest language, Stripe has an official Go SDK, and the acceptance probes hit the live HTTP API — they're language-agnostic. Deliberate, owned deviation.
- **Plans in DB, prices in config** — separating entitlements (queryable, webhook-updatable) from money math (pinned constants).
- **Pro = 10× Free** (10,000 calls / 1,000,000 tokens): predictable headroom.
- **Split `idempotency_keys` from `usage_events`** — one request can produce multiple usage events (api_call + ai_tokens), so the idempotency key belongs on its own one-row-per-request table; `usage_events` stays an append-only ledger.
- **Token categories as non-overlapping columns** (uncached input, cached input, output, reasoning) — Probe 5 needs per-category counts to price correctly; reasoning billed at the output rate in its own column (never double-counted).
- **Single-transaction exactly-once** with `INSERT ... ON CONFLICT DO NOTHING RETURNING` to claim the key — avoids the aborted-transaction trap of catching a raw unique violation and querying within the same failed transaction. Claim first, do work second; no `processing` state, no stuck records.
- **Deferred to Phase 2:** the quota read-then-write race (Probe 2) — to be closed with an atomic per-tenant counter row.

**Where AI helped:** Used Claude as a design coach. It ran my proposed architecture against the five acceptance probes and surfaced two refinements I agreed with: (1) splitting idempotency from usage events, (2) recording token categories as separate columns. It also drafted DESIGN.md from the decisions above. Every architectural choice was mine; I can explain each one.

**Where AI was wrong / what I corrected:** (none yet — to be logged as it happens.)

## 2026-11-03 — Phase 2.1: Database migrations

**What I did:** Wrote the PostgreSQL schema as `golang-migrate` migrations (`migrations/000001_core_schema.*`, `000002_seed_plans.*`) — 8 tables: tenants, plans, subscriptions, tenant_api_keys, idempotency_keys, usage_events, usage_counters, processed_stripe_events. HTTP handlers and the Go repository layer are intentionally NOT in this change (next sub-task).

**Stack deviation (still explicit):** the brief suggests Node/Python; we build in **Go**. Migration SQL is tool-agnostic; the only Go-specific choice is the migration runner (`golang-migrate`, plain `.up.sql`/`.down.sql`). Decision unchanged and owned.

**Decisions I made (and why):**
- **`usage_counters(tenant_id, period)`** as a fast, race-free quota gate, separate from the immutable `usage_events` ledger. Period = `'YYYY-MM'` (UTC) → automatic monthly reset with no cron, no boundary race.
- **Atomic quota gate** = ensure-row (`INSERT ... ON CONFLICT DO NOTHING`) then a single guarded `UPDATE ... WHERE used + req <= limit RETURNING`, checking API-call and token limits **together**. Row-lock + `WHERE` re-evaluation under `READ COMMITTED` prevents concurrent overshoot. Counter + ledger written in the same transaction, so rollback reverts both.
- **API keys hashed with SHA-256**, not bcrypt — keys are high-entropy secrets that must be looked up O(1); bcrypt is salted and unsearchable. (bcrypt stays for passwords elsewhere.)
- **`ck_usage_dimension` CHECK** enforces one usage dimension per ledger row; non-negative CHECKs on all quantities; token categories kept non-overlapping (uncached input vs cached input; reasoning in its own column at output rate).
- **Every tenant gets a Free, active subscription at creation** so quota lookup always resolves; `402` is reserved for canceled/past_due, not a missing row.

**Where AI helped:** Claude reviewed the proposed schema against the acceptance probes and surfaced weaknesses I agreed with — the guarded-UPDATE "no row = exceeded vs missing row" ambiguity (fixed by ensure-row-first), the single-statement upsert bypassing the guard on first insert (fixed by the two-statement pattern), SHA-256-vs-bcrypt for API keys, and request-hash normalization. It drafted the migration SQL from these decisions; every choice was mine and I can defend each one.

**Where AI was wrong / what I corrected:** (none this round.)

**Review refinements (my call, post-PR-#1 review):** tightened the schema before merging — `UNIQUE(idempotency_key_id, type)` (one event per dimension); composite FK `(idempotency_key_id, tenant_id) → idempotency_keys(id, tenant_id)` so a usage event's tenant is pinned to its idempotency record; `ON DELETE RESTRICT` on both `usage_events` FKs so history can't be silently erased; `api_call` fixed at `quantity = 1`; `ck_period_format` for `YYYY-MM`; `ck_idem_response_consistency` (response fields all-NULL or all-set).

**Validation (2026-11-03):** ran the migrations against a disposable PostgreSQL 16.15 container via golang-migrate. `up` → version 2; all 8 tables + constraints + indexes verified against the catalog; 6 negative tests confirmed each new constraint rejects its violation; `down -all` then `up` proved reversibility and deterministic re-seed. Full results in [EVIDENCE.md](EVIDENCE.md). The container was torn down afterward. Go unit tests for the response-integrity invariant are deferred to Phase 2.2 (repository layer).

**Final review hardening (2026-11-05, my call):** three more refinements before merge.
- **Commit-time idempotency completeness** — added `trg_idem_require_complete`, a `DEFERRABLE INITIALLY DEFERRED` constraint trigger that rejects an idempotency record committed without its response. The NULL-filled claim is still allowed *mid-transaction* (claim-first works); only an *incomplete commit* fails. The function re-reads the row by PK so it judges the final committed state, not a stale trigger `NEW` image.
- **Safe seed rollback** — `000002_seed_plans.down.sql` deletes a seeded plan only when no subscription references it, so rolling back the seed can't strand a live tenant; `up` re-asserts via `ON CONFLICT DO UPDATE`.
- **Append-only clarification** — documented that `ON DELETE RESTRICT` protects against parent-cascade erasure but not direct `UPDATE`/`DELETE`; the repository/service layer must expose INSERT + read only for `usage_events`.

Re-validated on a fresh disposable Postgres: deferred-trigger success/fail cases, safe seed rollback with a referenced Free plan preserved, and full reversibility of the trigger/function migration. Results in [EVIDENCE.md](EVIDENCE.md); container torn down.

## 2026-11-05 — Phase 2.2: service foundation

**What I did:** Stood up the Go service skeleton — config, database pool, migration runner, health/readiness, graceful shutdown. No billing endpoint, no quota, no Stripe yet.

**Layering (transport → services → repositories → PostgreSQL):**
- `cmd/api` — composition root: load config → open pool → build server → run → graceful shutdown. Does NOT migrate on startup.
- `cmd/migrate` — explicit golang-migrate runner (`make migrate-up`), separate from the API.
- `internal/config` — the only package reading env; typed + validated.
- `internal/database` — owns the pgx pool (tuning + `Ping` + `Close`); the only package touching the driver.
- `internal/httpapi` — transport only; `/health` (liveness) and `/ready` (readiness via a narrow `Pinger` interface, **no SQL in the handler**).

**Decisions (mine, from the architecture review):**
- stdlib `net/http` ServeMux (Go 1.22+ method routing) — no router dependency.
- Migrations are an explicit step, not run on API boot (workflow: db-up → migrate-up → run → /ready). A one-shot migration container can come later for prod.
- Compose runs Postgres only; the API runs locally via `go run ./cmd/api`. Dockerfile deferred until the foundation is stable.
- Made the published Postgres host port configurable (`POSTGRES_HOST_PORT`, default 5432) after hitting a local 5432 conflict — keeps 5432 as the clean-machine default while letting a dev override it.

**Where AI helped:** Claude proposed the package layout and the `Pinger`-interface approach for readiness (keeps SQL out of handlers), wrote the scaffolding from my three decisions, and ran the end-to-end validation. Decisions were mine; I can explain each package's responsibility.

**Where AI was wrong / what I corrected:** (none; the only surprise was the host 5432 port conflict, fixed by making the port configurable.)

**Validation:** `go build`/`go vet`/`go test ./...` green (config + httpapi unit tests; DB test gated). End-to-end on Docker Postgres: explicit migrate to v2, volume persistence across container recreate, connectivity integration test, `/health` 200, `/ready` 200→503(DB down)→200(recovered), graceful shutdown. Results in [EVIDENCE.md](EVIDENCE.md).

**Review refinements (PR #2, 2026-11-05):** six hardening changes before merge.
1. Strict config — malformed/out-of-range env values are hard errors now (env loader records the first parse error), plus PORT range, pool bounds, and positive-duration checks; tests added.
2. Safer rollback — a bare `migrate down` refuses; `down N` or explicit `down-all` (with a warning) required. Full rollback is never the default.
3. `make test-integration` fails fast with a clear message when DATABASE_URL is empty, instead of silently skipping.
4. `make db-up` health wait is bounded (HEALTH_TIMEOUT, 60s) and dumps container logs + exits non-zero on timeout.
5. Documented the fail-fast startup policy (DB down at startup → API won't start; DB down later → /ready 503, process stays up).
6. Consistency — `cmd/migrate` reads the DB URL via `config.DatabaseURL()`, so `internal/config` really is the only package reading env; comment corrected.

One self-inflicted snag during validation (not a code bug): I ran a stale `bin/api` before rebuilding, and the old binary started a server that didn't exit, hanging the script. Killed it (by exact process name, not `-f`, to avoid self-matching), rebuilt, and the new binary fails fast as intended. Validation results in [EVIDENCE.md](EVIDENCE.md).

## 2026-11-05 — Phases 2.3–8: full billing engine

Merged PR #2, then built the rest on `feature/complete-billing-engine`.

**What I built:** API-key auth (SHA-256), idempotent `POST /v1/generate` with atomic quota, `GET /v1/usage`, pinned-rate cost engine, Stripe test-mode checkout + verified/deduped webhooks, a background reconciliation job, and the full Docker stack.

**Key decisions (mine):**
- **Config overflow fix before merging PR #2** — range-check ints before int32/Duration casts, per review.
- **Single READ COMMITTED transaction** for metering: claim (`ON CONFLICT DO NOTHING RETURNING`) → atomic guarded counter `UPDATE` → ledger inserts → store response, all commit/rollback together. The unique index is the serialization point; proven no-overshoot under 30 concurrent requests.
- **Cost model = Gemini 2.5 Flash, Standard tier**, rates verified from the official pricing page (cited in `internal/money/pricing.go`, verified 2026-11-05): input $0.30/1M, cached $0.03/1M, output $2.50/1M, reasoning at output rate. Integer micro-USD, per-category floor. API-call price is 0 (Gemini bills by tokens) — documented, not invented.
- **Webhooks native-verified via the Stripe Go SDK** (`webhook.ConstructEventWithOptions`), parsed from `Data.Raw` to stay version-stable; dedup on `event.id`.
- **Reconciliation** detects drift only (never mutates the ledger); bounded retry + backoff + structured alert; graceful stop.
- **Stripe optional** — nil deps make checkout/webhook endpoints 503; live keys refused (test-mode only).

**Where AI helped:** Claude implemented the layered packages, the metering transaction, the cost engine, the Stripe integration and the reconciliation job from my decisions, wrote the tests, and ran all validation. It fetched and cited the live Gemini rates rather than inventing them. Every design choice was mine and is explained here.

**Where AI was wrong / what I corrected (honest):**
- **jsonb replay bug:** the idempotency response was stored as `jsonb`, which re-orders keys — replays were semantically but not byte-identical. Caught it in the first live run; added migration `000003` to store `response_body` as `text` for exact replay.
- **Dockerfile ENTRYPOINT bug:** I first used `ENTRYPOINT ["/app/api"]`, so the compose `command: ["/app/migrate","up"]` became args to the api binary and the "migrate" service actually ran the API (hung the stack). Fixed by switching to `CMD`. A leftover dirty-schema state from that buggy run also caused a one-off migrate exit 1, cleared by `down -v`.
- Both were caught by actually running the system, not by assuming it worked.

**Validation:** `gofmt`/`build`/`vet`/`test`/`-race` all green; integration tests on disposable Postgres; `docker compose up --build` boots postgres → one-shot migrate → non-root API; all five acceptance probes have evidence. See [EVIDENCE.md](EVIDENCE.md).

**Remaining external step:** live Stripe Checkout session creation needs real test keys (implemented + mock-tested; webhook path fully tested locally).

## Implementation checklist

- [x] Phase 1 — design (merged)
- [x] Phase 2.1 — migrations: 8 tables, deferred completeness trigger, safe seed rollback (merged)
- [x] Phase 2.2 — service foundation: config, pgx pool, migrate runner, health/ready, graceful shutdown (merged)
- [x] Phase 2.3 — auth, idempotent metering, atomic quota, usage reporting
- [x] Phase 3 — Stripe test-mode checkout + verified/deduped webhooks
- [x] Phase 4 — cost engine (cited Gemini rates, integer micro-USD)
- [x] Phase 7 — background reconciliation job (retry + alert)
- [x] Phase 8 — multi-stage Docker image, one-shot migrate service, one-command boot
- [x] Tests: unit + integration + `-race`; five acceptance probes
- [x] Docs: README, DESIGN, EVIDENCE, BUILDLOG, capstone.yaml, .env.example
- [ ] External: live Stripe Checkout end-to-end (needs test account; implemented + mock-tested)

## 2026-11-05 — PR #3 review corrections

**1. Reconciliation strengthened (my fix, from review).** The DBChecker used a LEFT JOIN from counters → ledger, so it missed **ledger-only** drift (ledger rows with no counter row), and its single `LIMIT batchSize` only ever scanned the first page. Rewrote it to:
- **FULL OUTER JOIN** counters ↔ ledger aggregate, flagging both counter-only and ledger-only mismatches;
- **keyset pagination** over `(tenant_id, period)` (cursor = last row), so every tenant/period is examined each run with no skip or repeat;
- **alert on an actual discrepancy**, not only on query failure after retries.
Kept it strictly read-only on `usage_events`, and preserved bounded retry + backoff and graceful stop. Added tests: counter-only, ledger-only, multi-page (batchSize 2 < 5 discrepancies, each seen exactly once), discrepancy→alert, clean→no-alert, transient→recover, retry-exhaustion→alert.

**2. Stripe evidence corrected.** EVIDENCE.md now distinguishes three cases honestly: (a) Checkout session-creation logic — PROVEN via mock; (b) locally-signed webhook verification + subscription sync — PROVEN; (c) real **hosted** Stripe Checkout + live webhook end-to-end — **PENDING external credentials, not yet run**. Probe 3's hosted-Checkout half is no longer marked complete; the webhook→Pro→usage half remains proven.
