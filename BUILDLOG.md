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
