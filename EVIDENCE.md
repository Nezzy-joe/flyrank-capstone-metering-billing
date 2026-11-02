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
