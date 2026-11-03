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
