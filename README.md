# Usage Metering & Billing Engine

A backend service that answers the three questions every SaaS must: **how much has a customer used, what does it cost, and have they hit their plan limits?** Metering with exactly-once guarantees, quota enforcement with honest status codes, AI-token cost calculation, and Stripe (test mode) subscription sync via signature-verified, idempotent webhooks.

**Stack:** Go · PostgreSQL · Docker Compose · Stripe (test mode) · Stripe CLI

> **Status:** Phase 2.2 (service foundation) — config, pgx pool, migration runner, `/health` + `/ready`, graceful shutdown. Billing endpoints come next. See [DESIGN.md](DESIGN.md).

## Plans

| Plan | API calls / month | AI tokens / month |
|------|-------------------|-------------------|
| Free | 1,000             | 100,000           |
| Pro  | 10,000            | 1,000,000         |

## Architecture

See [DESIGN.md](DESIGN.md) for the full data model, API contract, transaction/concurrency strategy, and non-goals. (Architecture diagram added in Phase 4.)

## Run (development)

Migrations are an explicit step, kept out of API startup. The workflow is: start Postgres → migrate → run the API → check readiness.

```bash
cp .env.example .env     # .env is git-ignored. If 5432 is taken, set POSTGRES_HOST_PORT + DATABASE_URL to e.g. 5434
make db-up               # start PostgreSQL (Docker) and wait for health
make migrate-up          # apply migrations (golang-migrate)
make run                 # run the API locally: go run ./cmd/api
# in another shell:
make ready               # GET /ready -> 200 once the DB is reachable
```

Tests: `make test` (unit, no DB) · `make test-integration` (adds DB connectivity; needs `make db-up`).

> A multi-stage Dockerfile + an `api` compose service will be added once the foundation is stable, so `docker compose up --build` can boot the whole service in one command (per the capstone).

Base URL: `http://localhost:8080`

## Endpoints

| Method | Path                 | Purpose                               |
|--------|----------------------|---------------------------------------|
| GET    | `/health`            | Liveness (process up)                 |
| GET    | `/ready`             | Readiness (verifies DB connectivity)  |
| POST   | `/v1/generate`       | Billable action: meter + quota + cost |
| GET    | `/v1/usage`          | Current-period usage/cost rollup      |
| POST   | `/v1/checkout`       | Stripe test Checkout (Free → Pro)     |
| POST   | `/webhooks/stripe`   | Subscription sync (verified, idempotent) |

## Limitations

Stripe test mode only; AI tokens are simulated; no invoicing/proration/overage in core; minimal API-key auth; USD and calendar-month period only. See non-goals in [DESIGN.md](DESIGN.md#8-explicit-non-goals-what-this-does-not-do).
