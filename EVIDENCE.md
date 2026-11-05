# Evidence

One pasted proof per Requirements checkbox (Section 6 of the brief) and per acceptance probe (Section 12). Filled as each phase lands. `[ ]` = pending, `[x]` = proven.

## Metering
- [ ] A billable action creates exactly one usage event, even under retries — deduplicated by idempotency key.
- [ ] Proof that double-counting cannot happen: same request sent twice → one usage event.

## Quotas
- [ ] Usage checked against the tenant's plan; over-limit requests rejected.
- [ ] Correct status codes (429 / 402) with a clear explaining message.

## Cost calculation
- [ ] Monthly usage rolls up into a cost figure per tenant.
- [ ] Token pricing handles cached-input, reasoning, and output correctly.
- [ ] Pricing constants pinned in config; worked totals shown here.

## Stripe integration
- [ ] Checkout works end-to-end in test mode.
- [ ] Webhooks verify signatures, ignore duplicate events, update plan/status.

## Data model, tests & documentation
- [ ] tenants, plans, subscriptions, usage_events present; tenant data isolated.
- [ ] README + architecture diagram + setup; required files present.

## Acceptance probes
- [ ] **Probe 1** — same request twice + one key → one usage event; second response mirrors the first.
- [ ] **Probe 2** — tenant driven to exact quota → boundary behaves per rule; next returns 429/402.
- [ ] **Probe 3** — Stripe test Checkout → webhook flips Free → Pro → GET /usage shows new limits.
- [ ] **Probe 4** — forged webhook → 400, nothing changes; real event replayed twice → processed once.
- [ ] **Probe 5** — pinned pricing → cached-input and reasoning rules produce exact totals; GET /usage matches.

---

## Phase 2.1 — migration validation (2026-11-03)

Validated against a disposable **PostgreSQL 16.15** container, migrations applied with **golang-migrate** (official `migrate/migrate` image). Not behavioral probes (those need the Phase 2.2 handlers) — this proves the schema applies, the constraints enforce the invariants, and the migrations are reversible.

**Apply:** `migrate up` → `1/u core_schema`, `2/u seed_plans`; `migrate version` → `2`.

**Structure present (via `pg_tables` / `pg_constraint` / `pg_indexes`):**
- 8 app tables + `schema_migrations` (9 total).
- `usage_events`: 2 FKs — `fk_usage_tenant` (RESTRICT) and composite `fk_usage_idem (idempotency_key_id, tenant_id) → idempotency_keys(id, tenant_id)` (RESTRICT); `uq_usage_request_dimension UNIQUE(idempotency_key_id, type)`; `ck_usage_dimension` (api_call ⇒ quantity=1 & all tokens 0; ai_tokens ⇒ quantity=0).
- `idempotency_keys`: `uq_idem_tenant_key`, `uq_idem_id_tenant`, `ck_idem_response_consistency`.
- `usage_counters`: `ck_period_format CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$')`.
- Plans seeded: `free` (1000 / 100000), `pro` (10000 / 1000000).

**Constraints proven to bite (negative tests — each rejected as expected):**

| Refinement | Attempted violation | Result |
|---|---|---|
| R1 one-per-dimension | 2nd `api_call` for same request | rejected — `uq_usage_request_dimension` |
| R2 tenant isolation | event for tenant B pointing at A's idempotency record | rejected — `fk_usage_idem` |
| R3 append-only ledger | `DELETE` a tenant that has usage events | rejected — `fk_usage_tenant` RESTRICT |
| R4 api_call quantity | `ai_tokens` row with `quantity = 1` | rejected — `ck_usage_dimension` |
| R5 period format | `usage_counters.period = '2026-13'` | rejected — `ck_period_format` |
| R7 response integrity | idempotency row with `response_status` set but body/completed_at NULL | rejected — `ck_idem_response_consistency` |

**Positive control:** one valid `api_call` + one valid `ai_tokens` event for the same request were both accepted.

**Reversibility:** `migrate down -all` dropped all app tables (only `schema_migrations` remained); `migrate up` rebuilt all 8 and reseeded plans deterministically → version `2`.

> Deferred to Phase 2.2 (not started here): Go unit tests for the response-integrity invariant, and the behavioral probes (duplicate→one event, concurrent→no overshoot) which need the repository + handler layer.

### Phase 2.1 — final review hardening (2026-11-05)

Re-validated against a fresh disposable PostgreSQL 16 container via golang-migrate after three review refinements.

**Commit-time idempotency completeness (deferred constraint trigger).** Added `trg_idem_require_complete` (`DEFERRABLE INITIALLY DEFERRED`, confirmed `tgdeferrable=t, tginitdeferred=t`) backed by `idem_require_complete_at_commit()`, which re-reads the row by PK and requires `response_status + response_body + completed_at` at commit.

| Test | Expected | Result |
|---|---|---|
| Claim → store response → `COMMIT` | success | ✅ committed, row fully populated |
| Claim only → `COMMIT` without completing | fail at commit | ✅ rejected: *"response incomplete at commit"*; row count after = 0 (rolled back) |
| Mid-transaction NULL claim visible before commit | allowed | ✅ row present with NULL response inside the txn |
| Partial row (status set, body/completed_at NULL) at statement time | fail immediately | ✅ rejected by `ck_idem_response_consistency` |

**Safe seed rollback preserving referenced plans.** `000002_seed_plans.down.sql` now deletes a seeded plan only when no subscription references it.

| Step | Result |
|---|---|
| `up` (v2), insert tenant + subscription on Free | ok |
| `down 1` (roll back seed only) | version → **1**; Free **preserved** (referenced), Pro deleted |
| `up` (reapply seed) | version → **2**; Free 1000/100000 and Pro 10000/1000000 both correct; subscription intact |

**Reversibility with the new objects.** `down -all` → trigger function count 0, only `schema_migrations` remains; `up` → function recreated, version 2.

**Append-only clarification (docs).** `ON DELETE RESTRICT` blocks parent-cascade erasure but not direct `UPDATE`/`DELETE` of `usage_events`; the repository/service layer must expose only INSERT + read paths. Documented in `DESIGN.md` and in the migration comment. (DB-role `REVOKE` noted as optional Phase-4 hardening.)

#### Command / output excerpts

Engine: PostgreSQL 16 in a disposable Docker container; migrations run with the official `migrate/migrate` image.

`migrate up` and the deferred trigger present and marked deferrable:

```console
$ migrate -path=/migrations -database "$DBURL" up
1/u core_schema (407ms)
2/u seed_plans (443ms)

$ psql -c "SELECT tgname,tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgname='trg_idem_require_complete';"
          tgname           | tgdeferrable | tginitdeferred
---------------------------+--------------+----------------
 trg_idem_require_complete | t            | t
```

Deferred completeness — **Test B** (claim only, then COMMIT without completing): the NULL claim is visible inside the txn, the COMMIT is rejected, and the row rolls back to 0:

```console
BEGIN
INSERT 0 1
                    note                    | count
--------------------------------------------+-------
 mid-txn row exists (NULL response allowed) |     1
ERROR:  idempotency_keys b2222222-0000-0000-0000-000000000002: response incomplete at commit (store response_status+response_body+completed_at before COMMIT)
CONTEXT:  PL/pgSQL function idem_require_complete_at_commit() line 10 at RAISE
           note           | count
--------------------------+-------
 rows after failed commit |     0
```

Safe seed rollback — Free referenced by a subscription is preserved, unreferenced Pro is removed; reapply restores both:

```console
$ migrate down 1
2/d seed_plans (40ms)
$ psql -c "SELECT id,api_call_limit,ai_token_limit FROM plans ORDER BY id;"
  id  | api_call_limit | ai_token_limit
------+----------------+----------------
 free |           1000 |         100000      -- referenced -> preserved (pro deleted)

$ migrate up            # reapply the seed
2/u seed_plans (33ms)
$ psql -c "SELECT id,api_call_limit,ai_token_limit FROM plans ORDER BY id;"
  id  | api_call_limit | ai_token_limit
------+----------------+----------------
 free |           1000 |         100000
 pro  |          10000 |        1000000
```

Constraint negative tests (round 1) — each violation rejected by the intended constraint:

```console
R1 2nd api_call for same request  -> ERROR: duplicate key value violates unique constraint "uq_usage_request_dimension"
R2 event for tenant B on A's idem -> ERROR: insert or update on table "usage_events" violates foreign key constraint "fk_usage_idem"
R3 delete tenant that has events  -> ERROR: update or delete on table "tenants" violates foreign key constraint "fk_usage_tenant" on table "usage_events"
R4 ai_tokens row with quantity=1  -> ERROR: new row for relation "usage_events" violates check constraint "ck_usage_dimension"
R5 usage_counters.period='2026-13'-> ERROR: new row for relation "usage_counters" violates check constraint "ck_period_format"
R7 partial idempotency response   -> ERROR: new row for relation "idempotency_keys" violates check constraint "ck_idem_response_consistency"
```

---

## Phase 2.2 — service foundation (2026-11-05)

Go service skeleton: config, pgx pool, explicit migration runner, `/health` + `/ready`, graceful shutdown. No billing endpoint / quota / Stripe yet.

**Build + unit tests:**
```console
$ go build ./... && go vet ./...        # OK
$ go test ./...
ok  internal/config    0.004s
ok  internal/database  0.005s   # connectivity test skips without TEST_DATABASE_URL
ok  internal/httpapi   0.002s   # /health 200, /ready 200 & 503, method routing
```

**End-to-end against Docker Postgres (host port configurable; used 5434 here):**
```console
# explicit migrations (separate step)
$ go run ./cmd/migrate version    -> none (no migrations applied)
$ go run ./cmd/migrate up         -> migrate up: done
$ go run ./cmd/migrate version    -> version: 2 (dirty=false)

# persistent volume: recreate the container, data survives
$ docker compose down && docker compose up -d
$ go run ./cmd/migrate version    -> version: 2 (dirty=false)

# DB connectivity integration test
=== RUN   TestNew_Connectivity
--- PASS: TestNew_Connectivity (0.01s)

# live endpoints (API via go build -o bin/api ./cmd/api)
$ curl /health   -> {"status":"ok"}                                   [HTTP 200]
$ curl /ready    -> {"status":"ready"}                                [HTTP 200]
$ docker compose stop postgres
$ curl /ready    -> {"status":"unavailable","reason":"database not reachable"} [HTTP 503]
$ docker compose start postgres
$ curl /ready    -> {"status":"ready"}                                [HTTP 200]

# graceful shutdown (SIGTERM)
listening on :8080 (env=development)
shutdown signal received
server stopped cleanly
```

Satisfies the Phase 2.2 asks: env-based config, pgx pooling, golang-migrate runner, Docker healthcheck + persistent volume, `/health`, `/ready` (real DB check), graceful shutdown, and tests for config + connectivity + health/readiness. No SQL in handlers (readiness goes through the `Pinger` interface).
