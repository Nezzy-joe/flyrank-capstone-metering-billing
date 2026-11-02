# Usage Metering & Billing Engine

A backend service that answers the three questions every SaaS must: **how much has a customer used, what does it cost, and have they hit their plan limits?** Metering with exactly-once guarantees, quota enforcement with honest status codes, AI-token cost calculation, and Stripe (test mode) subscription sync via signature-verified, idempotent webhooks.

**Stack:** Go · PostgreSQL · Docker Compose · Stripe (test mode) · Stripe CLI

> **Status:** Phase 1 (Design) complete — see [DESIGN.md](DESIGN.md). Implementation in progress.

## Plans

| Plan | API calls / month | AI tokens / month |
|------|-------------------|-------------------|
| Free | 1,000             | 100,000           |
| Pro  | 10,000            | 1,000,000         |

## Architecture

See [DESIGN.md](DESIGN.md) for the full data model, API contract, transaction/concurrency strategy, and non-goals. (Architecture diagram added in Phase 4.)

## Run (to be completed in Phase 2)

```bash
cp .env.example .env    # fill in values; .env is git-ignored
docker compose up --build
# seed demo data:
# make seed    (TODO Phase 2)
```

Base URL: `http://localhost:8080`

## Endpoints

| Method | Path                 | Purpose                               |
|--------|----------------------|---------------------------------------|
| POST   | `/v1/generate`       | Billable action: meter + quota + cost |
| GET    | `/v1/usage`          | Current-period usage/cost rollup      |
| POST   | `/v1/checkout`       | Stripe test Checkout (Free → Pro)     |
| POST   | `/webhooks/stripe`   | Subscription sync (verified, idempotent) |

## Limitations

Stripe test mode only; AI tokens are simulated; no invoicing/proration/overage in core; minimal API-key auth; USD and calendar-month period only. See non-goals in [DESIGN.md](DESIGN.md#8-explicit-non-goals-what-this-does-not-do).
