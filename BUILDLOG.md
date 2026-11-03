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
