# Usage Metering & Billing Engine

A backend service that answers the three questions every SaaS must: **how much has a customer used, what does it cost, and have they hit their plan limits?** It meters billable actions with exactly-once guarantees, enforces per-plan quotas with honest status codes, calculates cost under real AI-token pricing rules, and syncs subscriptions from Stripe (test mode) via signature-verified, idempotent webhooks.

**Stack:** Go · PostgreSQL (pgx) · Docker Compose · Stripe test mode · golang-migrate

> **Language note:** the FlyRank brief suggests JavaScript or Python; this project is built in **Go** by deliberate choice (strongest language; Stripe has an official Go SDK; the acceptance probes are HTTP-level and language-agnostic). This deviation is documented here, in `DESIGN.md`, and in `BUILDLOG.md`. It has **not** been approved by FlyRank — it is an owned engineering decision.

## Features

- **API-key auth** — `Authorization: Bearer <key>`; only SHA-256 hashes are stored; revocable; `401` on invalid/missing.
- **Idempotent metering** — `POST /v1/generate` records exactly one `api_call` + one `ai_tokens` event per request, safe under retries and concurrency.
- **Atomic quota enforcement** — one guarded `UPDATE` checks API-call and token limits together; concurrent requests can never overshoot. `429` over limit, `402` without an active subscription.
- **Cost calculation** — integer micro-USD, pinned Gemini 2.5 Flash rates; cached input cheaper than input, reasoning billed at the output rate, categories priced independently.
- **Usage reporting** — `GET /v1/usage` returns plan, used/remaining/limit per dimension, and current-period cost.
- **Stripe test mode** — `POST /v1/checkout` (Pro upgrade) and `POST /webhooks/stripe` (signature-verified, deduplicated, transactional subscription sync).
- **Background reconciliation** — a job compares counters to the immutable ledger with bounded retry + failure alerting.
- **Operational** — `/health` (liveness), `/ready` (DB readiness), graceful shutdown, structured logs.

## Architecture

Layered: **HTTP transport → services → repositories → PostgreSQL**. Handlers parse/validate and map errors to status codes; services own business rules and transaction boundaries; repositories own SQL; `internal/config` owns env; `internal/database` owns the pgx pool.

```
            Bearer key              ┌─────────────┐
 client ───────────────▶ httpapi ──▶│  services   │──▶ repository ──▶ PostgreSQL
                         (transport) │ meter/usage │    (all SQL)       (8 tables)
   Stripe ──signed webhook──▶        │ auth/billing│
                                     │ reconcile   │──(background job)
                                     └─────────────┘
```

See [DESIGN.md](DESIGN.md) for the data model, the single-transaction metering flow, the atomic-quota mechanism, and the startup/readiness policy.

## Configuration

Copy `.env.example` to `.env` (git-ignored). Key variables:

| Variable | Purpose | Default |
|---|---|---|
| `DATABASE_URL` | Postgres connection string | — (required) |
| `PORT` | HTTP port | `8080` |
| `POSTGRES_HOST_PORT` | Host port Docker publishes Postgres on | `5432` |
| `STRIPE_SECRET_KEY` | Stripe **test** key (`sk_test_…`); live keys are rejected | empty (Stripe off) |
| `STRIPE_WEBHOOK_SECRET` | Webhook signing secret (`whsec_…`) | empty |
| `STRIPE_PRICE_PRO` | Stripe Price ID for the Pro plan | empty |
| `PUBLIC_BASE_URL` | Checkout success/cancel base URL | `http://localhost:8080` |
| `RECONCILE_INTERVAL_SECONDS` | Background job interval (`0` disables) | `3600` |

Money is integer micro-USD. No secret is ever logged; `.env` is never committed.

## Run — Docker (one command)

```bash
cp .env.example .env
docker compose up --build        # postgres (healthcheck+volume) -> one-shot migrate -> api
```

Seed demo data (prints a usable API key **once**):

```bash
docker compose run --rm api /app/seed
```

The API is at `http://localhost:8080`. A multi-stage image produces a small, non-root runtime; migrations run as a **separate one-shot service** (the API never migrates on startup), and the API waits for both database health and migration success.

## Run — local dev (API on host)

```bash
cp .env.example .env     # if 5432 is taken, set POSTGRES_HOST_PORT + DATABASE_URL to e.g. 5434
make db-up               # start PostgreSQL only
make migrate-up          # apply migrations (explicit step)
make seed                # demo tenant + API key (once)
make run                 # go run ./cmd/api
```

Migrations: `make migrate-up` · `make migrate-down N=1` · `make migrate-down-all` (destructive, explicit).

## API

All `/v1/*` routes require `Authorization: Bearer <api_key>`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Liveness |
| GET | `/ready` | Readiness (verifies DB) |
| POST | `/v1/generate` | Billable action: meter + quota + cost (needs `Idempotency-Key`) |
| GET | `/v1/usage` | Current-period usage + cost |
| POST | `/v1/checkout` | Stripe test Checkout (Free → Pro) |
| POST | `/webhooks/stripe` | Subscription sync (signature-verified) |

### Examples

```bash
# meter usage (retry-safe via Idempotency-Key)
curl -X POST localhost:8080/v1/generate \
  -H "Authorization: Bearer $KEY" -H 'Idempotency-Key: req-1' \
  -d '{"input_tokens":1000,"cached_input_tokens":300,"output_tokens":2500,"reasoning_tokens":200}'

# usage
curl localhost:8080/v1/usage -H "Authorization: Bearer $KEY"

# start a Pro upgrade (requires Stripe test config) -> returns {"checkout_url": "..."}
curl -X POST localhost:8080/v1/checkout -H "Authorization: Bearer $KEY"
```

Responses: `200` success · `400` bad request · `401` bad key · `402` no active subscription · `409` idempotency key reused with a different body · `429` quota exceeded.

## Testing

```bash
make test               # unit tests (no DB)
go test -race ./...     # race detector
make db-up && make migrate-up && make test-integration   # adds DB integration tests
```

Integration tests (idempotency, concurrency, quota, webhooks, reconciliation) use a real Postgres and skip automatically when `TEST_DATABASE_URL` is unset. See [EVIDENCE.md](EVIDENCE.md) for captured results and the five acceptance probes.

## Stripe test-mode setup

1. Create a Stripe account; stay in **Test mode**.
2. Create a Product + recurring Price for "Pro"; copy the Price ID into `STRIPE_PRICE_PRO`.
3. Put your test secret key in `STRIPE_SECRET_KEY` (`sk_test_…`).
4. Forward webhooks locally with the Stripe CLI and copy the signing secret into `STRIPE_WEBHOOK_SECRET`:
   ```bash
   stripe listen --forward-to localhost:8080/webhooks/stripe
   stripe trigger checkout.session.completed
   ```

No live keys, ever — the service rejects a non-`sk_test_` key at startup. Test cards like `4242 4242 4242 4242` work in Checkout.

## Security notes

- API keys stored as SHA-256 hashes only; raw keys shown once at seed time, never logged.
- Stripe secrets come from env; webhook signatures verified against the **raw** body; `.env` git-ignored.
- Money as integers (no float); tenant isolation enforced by `tenant_id` foreign keys and query scoping; the usage ledger is append-only (DB `RESTRICT` + INSERT/read-only repository).

## Limitations

- Stripe Checkout session creation requires live test credentials to exercise end-to-end; it is covered by a mock-based unit test here, with the external step documented (see EVIDENCE.md). Webhook verification/sync is fully tested locally.
- AI tokens are simulated (no model is called).
- No invoicing, proration, or overage billing (explicit non-goals).
- Minimal auth (API keys; no user accounts/RBAC); single currency (USD); calendar-month (UTC) periods.

## Implementation checklist

See [BUILDLOG.md](BUILDLOG.md) for the honest build log (decisions, AI assistance, mistakes) and the stage-by-stage checklist.
