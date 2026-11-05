# Design — Usage Metering & Billing Engine

**Status:** Phase 1 (Design) · **Stack:** Go · PostgreSQL · Docker Compose · Stripe (test mode)
**Author:** Joseph Amos Ekpe

---

## 1. Problem

Every SaaS backend must answer three questions for each customer: **how much have they used, what should they pay, and have they hit their plan limits?** This service answers all three. It meters billable actions, enforces per-plan quotas with honest status codes, calculates cost under real-world AI-token pricing rules, and syncs subscription state from Stripe via signature-verified, idempotent webhooks.

The engineering heart of the system is **correctness under retries and concurrency**: the same request retried must be charged exactly once, a quota boundary must behave identically every time, and a replayed webhook must take effect once.

---

## 2. Plans & quotas

Quotas (entitlements) live in the **database**. Prices (money math) live in **application config**.

| Plan | API calls / month | AI tokens / month |
|------|-------------------|-------------------|
| Free | 1,000             | 100,000           |
| Pro  | 10,000            | 1,000,000         |

Pro is a deliberate 10× of Free: meaningful headroom, predictable limits.

---

## 3. Data model & relationships

```
tenants ─┬─< subscriptions >─ plans
         ├─< idempotency_keys ─< usage_events
         └─< processed_stripe_events
```

- **`tenants`** — one customer organization. All other rows are scoped by `tenant_id`; tenants never see each other's data.
- **`plans`** — `free` | `pro`, each with `api_call_limit` and `ai_token_limit`. Entitlements only.
- **`subscriptions`** — links a tenant to a plan, with `status` and the Stripe IDs. This is the row Stripe webhooks update. **Current-subscription model:** exactly one row per tenant (`UNIQUE(tenant_id)`) holding the *present* plan/status regardless of value — it mirrors current state, not history. Every tenant gets a Free, `active` subscription created **transactionally at registration** (application logic), so quota lookup always resolves; `402` is reserved for `canceled`/`past_due`.
- **`idempotency_keys`** — the request-dedup + response-replay record. **One row per request.**
- **`usage_events`** — the immutable, append-only billing ledger. **N rows per request** (e.g. one `api_call` + one `ai_tokens`), all referencing the same idempotency record.
- **`processed_stripe_events`** — webhook dedup; Stripe `event.id` processed at most once.

### Schema (Phase 1 sketch)

> **Post Phase-2.1 review, the migrations in `migrations/` are the source of truth.** Hardening added there: `UNIQUE(idempotency_key_id, type)` (one event per dimension), a composite FK pinning a usage event's tenant to its idempotency record, `ON DELETE RESTRICT` on the ledger (append-only), `api_call` quantity fixed at 1, a `YYYY-MM` period format CHECK, and a response-consistency CHECK. The sketch below shows the shape; see the SQL for exact constraints.

```sql
tenants(
  id            uuid PRIMARY KEY,
  name          text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);

plans(
  id              text PRIMARY KEY,          -- 'free' | 'pro'
  name            text NOT NULL,
  api_call_limit  bigint NOT NULL,
  ai_token_limit  bigint NOT NULL
);

subscriptions(
  id                      uuid PRIMARY KEY,
  tenant_id               uuid NOT NULL REFERENCES tenants(id),
  plan_id                 text NOT NULL REFERENCES plans(id),
  status                  text NOT NULL,     -- 'active' | 'canceled' | 'past_due'
  stripe_customer_id      text,
  stripe_subscription_id  text,
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id)                          -- one active subscription per tenant
);

idempotency_keys(
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  idempotency_key  text NOT NULL,
  request_hash     text NOT NULL,            -- hash of the normalized request body
  response_status  int,                       -- populated before COMMIT
  response_body    jsonb,                     -- populated before COMMIT
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, idempotency_key)
);

usage_events(
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  idempotency_key_id   uuid NOT NULL REFERENCES idempotency_keys(id),
  type                 text NOT NULL,        -- 'api_call' | 'ai_tokens'
  quantity             bigint NOT NULL DEFAULT 0,   -- used for 'api_call'
  -- AI-token categories (non-overlapping); used for 'ai_tokens'
  input_tokens         bigint NOT NULL DEFAULT 0,   -- UNCACHED input only
  cached_input_tokens  bigint NOT NULL DEFAULT 0,
  output_tokens        bigint NOT NULL DEFAULT 0,
  reasoning_tokens     bigint NOT NULL DEFAULT 0,
  created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON usage_events (tenant_id, created_at);

processed_stripe_events(
  event_id      text PRIMARY KEY,            -- Stripe event.id
  processed_at  timestamptz NOT NULL DEFAULT now()
);
```

---

## 4. Cost model (money math)

Money is stored as **integer micro-USD** (1 USD = 1,000,000 micro-USD); no floats. Token rates are pinned in config as micro-USD per **1,000,000 tokens** so all arithmetic stays integer. Categories are priced **independently** and summed — never added together as raw counts.

```
cost_micro =
    api_calls          * PRICE_PER_API_CALL
  + input_tokens       * RATE_INPUT         / 1_000_000
  + cached_input_tokens* RATE_CACHED_INPUT  / 1_000_000   -- cheaper than input
  + output_tokens      * RATE_OUTPUT        / 1_000_000
  + reasoning_tokens   * RATE_OUTPUT        / 1_000_000   -- reasoning billed at output rate
```

Rules encoded explicitly: **cached input is cheaper than uncached input**; **reasoning counts as output** (own column, output rate, never double-counted); **categories cannot simply be summed as tokens.** Exact constants are pinned in config in Phase 4, with worked totals proven in `EVIDENCE.md`.

---

## 5. API contract

Tenant is identified by an API key header, resolved to `tenant_id`; every query is scoped by `tenant_id` for isolation.

### `POST /v1/generate` — the billable endpoint
- **Headers:** `Authorization: Bearer <tenant_api_key>`, `Idempotency-Key: <key>` (required)
- **Body:** simulated token counts, e.g. `{ "input_tokens": 1000, "cached_input_tokens": 300, "output_tokens": 2500, "reasoning_tokens": 200 }`
- **200 OK:** `{ "usage": {...}, "cost_micro_usd": <int>, "quota": { "api_calls": {used, limit}, "ai_tokens": {used, limit} } }`
- **402 Payment Required:** no active subscription / plan does not permit the action.
- **409 Conflict:** same `Idempotency-Key`, **different** request body (hash mismatch).
- **429 Too Many Requests:** usage quota exceeded, with a clear message.
- **Retry with the same key + same body:** returns the **identical** first response; records **no** new usage.

**Quota boundary rule (documented, exact):** a request is allowed iff `used + requested <= limit`. So the call that brings usage to exactly the limit is **allowed**; the next call is rejected with `429`. Over-quota rejections are themselves stored on the idempotency record, so a retried rejected request replays the same `429`.

### `GET /v1/usage` — the read path
Returns the current-period rollup: `{ used, limits, cost }` per tenant, derived from `usage_events` and the pinned pricing constants.

### `POST /v1/checkout` — Stripe test Checkout (Phase 3)
Creates a Stripe Checkout session (test mode) for Free → Pro upgrade.

### `POST /webhooks/stripe` — subscription sync (Phase 3)
Verifies the signature (forged → `400`), deduplicates on `event.id` via `processed_stripe_events` (replay → ignored), updates the tenant's subscription plan/status. Handles `checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`.

---

## 6. Transaction & concurrency strategy

**Key insight:** in the metered path there is **no external side-effect** — AI tokens are simulated, Stripe is a separate path. So the entire billable operation fits in **one PostgreSQL transaction**, which gives true exactly-once for free: all-or-nothing commit, no partial state to recover.

**`POST /v1/generate` flow (single transaction):**

1. Resolve tenant from API key.
2. `BEGIN`.
3. **Claim the key:** `INSERT INTO idempotency_keys (...) ON CONFLICT (tenant_id, idempotency_key) DO NOTHING RETURNING id`.
   `ON CONFLICT DO NOTHING` **never raises**, so the transaction is never marked aborted — avoiding the "query after a unique violation in the same transaction" trap.
4. **If a row was returned (we are the one true executor):**
   - Check quota (see note below). If exceeded → build `429`/`402`, store it on the idempotency row, `COMMIT`, return. *(Rejections are idempotent too.)*
   - Else compute cost, `INSERT` the `usage_events` rows, build the `200` response, `UPDATE` the idempotency row with `response_status`/`response_body`, `COMMIT`, return.
5. **If no row was returned (key already claimed):**
   - `SELECT` the existing idempotency row (a fresh statement snapshot sees the committed winner).
   - `request_hash` matches → **replay** the stored response. Mismatch → `409`.

**Why concurrency is safe without an explicit lock:** two concurrent requests with the same key both attempt the claim `INSERT`; the second **blocks on the unique index** until the first commits or aborts. Commit → second gets "no row" and replays. Abort → second's insert succeeds and it becomes the executor. Because claim and response commit **together**, a visible idempotency row always has its response populated — so there is no `processing` state and no stuck-record failure mode. (If a real external charge were ever added to this path, we would switch to a two-phase `processing → completed` pattern plus a reconciliation job; we deliberately avoid needing that.)

**Deferred — Probe 2 quota race (Phase 2):** the quota check is a read-then-write. Two *different* requests can both read `used = 999` and both insert, overshooting the limit — the idempotency key does not help here because the requests differ. Phase 2 closes this with a per-tenant-per-period **counter row** updated atomically: `UPDATE ... SET used = used + :q WHERE used + :q <= :limit RETURNING`. `usage_events` remains the immutable ledger; the counter gives a race-free, single-statement quota gate. Design documented here; implementation in Phase 2.

---

## 7. Layered architecture

```
HTTP handlers (transport)      — parse, validate at the boundary, map errors → status codes
      │
Services (domain logic)        — MeterService, QuotaService, CostCalculator, BillingService
      │
Repositories (persistence)     — tenants, plans, subscriptions, idempotency, usage, events
      │
PostgreSQL
```

Validation happens at the boundary (bad input → clean `4xx`, never a `500`). Services hold the correctness rules and own the transaction boundary. Repositories are the only layer that touches SQL.

**Append-only ledger — scope of the DB guarantee.** `usage_events` carries `ON DELETE RESTRICT` on its foreign keys, which blocks *parent* deletion (a tenant or idempotency record) from erasing historical usage. This does **not** stop a direct `UPDATE` or `DELETE` of `usage_events` rows — SQL still permits those. True append-only is therefore a **two-layer** guarantee: the database prevents parent-cascade erasure, and the **repository/service layer must expose only `INSERT` + read paths for `usage_events` (no update, no delete)**. A dedicated DB role with `REVOKE UPDATE, DELETE ON usage_events` is noted as optional Phase-4 hardening.

**Idempotency completeness.** Two layers guard the idempotency record: an immediate `CHECK` (`ck_idem_response_consistency`) rejects *partial* rows at every statement, and a `DEFERRABLE INITIALLY DEFERRED` constraint trigger (`trg_idem_require_complete`) runs at **commit** and rejects an *incomplete* (all-NULL) claim. Together: a record may be NULL-filled only while its transaction is in flight; anything committed must carry `response_status` + `response_body` + `completed_at`.

**Startup & readiness policy (fail-fast).** `database.New` pings PostgreSQL *before* the HTTP server starts listening. The two states are deliberately different:
- **Database down at initial startup** → the API **does not start**. `database.New` returns an error, `main` exits non-zero. This is fail-fast: a misconfigured or unreachable database surfaces immediately rather than serving a half-working process, and a process manager / one-shot migration-then-start flow sees a clean failure.
- **Database down *after* startup** → the process **stays up** and `/ready` returns `503` (`/health` stays `200` since the process itself is alive). When the database returns, `/ready` goes back to `200` with no restart. This is what lets an orchestrator route traffic away from an instance during a transient DB outage and back when it recovers.

---

## 8. Explicit non-goals (what this does NOT do)

1. **No real payments** — Stripe **test mode only**; no real money ever moves.
2. **No real AI** — token counts are **simulated**; no model is called.
3. **No invoicing, proration, or overage billing** in the core — these are stretch goals.
4. **Minimal auth** — tenants are resolved by API key; no user accounts, roles, or RBAC.
5. **Single currency (USD) and a single calendar-month billing period.**
6. **No UI** — this is a backend service only.

> Primary explicit non-goal for the Phase 1 gate: **no invoicing, proration, or overage billing in the core.**

---

## 9. Implementation map (final)

```
cmd/api       composition root: config → pool → services → server → graceful shutdown (+ reconcile job)
cmd/migrate   explicit golang-migrate runner (up / down N / down-all / version)
cmd/seed      idempotent demo tenant + Free subscription + one-time API key

internal/config      typed, validated, env-only config (+ Stripe test-mode guard, overflow bounds)
internal/database    pgxpool (tuning, Ping, Close)
internal/apikey      crypto-random keys, SHA-256 hashing
internal/repository   ALL SQL behind a Querier (pool or tx)
internal/money       cost engine — pinned Gemini 2.5 Flash rates, integer micro-USD
internal/service     AuthService, MeterService (the metering transaction), UsageService
internal/billing     Stripe Gateway (checkout) + WebhookService (verify/dedup/sync)
internal/reconcile   background counter↔ledger reconciliation (retry + alert)
internal/httpapi     transport: auth middleware, handlers, error mapping (no SQL)
```

**Cost rates (pinned in `internal/money/pricing.go`):** Gemini 2.5 Flash, Standard tier — input $0.30/1M, cached input $0.03/1M, output $2.50/1M, reasoning at the output rate. Source: aistudio.google.com/docs/pricing, verified 2026-11-05. Cost is integer micro-USD with per-category floor; the API-call price is 0 (Gemini bills by tokens).

**Migrations:** `000001` core schema (8 tables + deferred completeness trigger), `000002` Free/Pro reference data, `000003` idempotency `response_body` → `text` (byte-identical replay).
