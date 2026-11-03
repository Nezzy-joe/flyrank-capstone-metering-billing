-- Phase 2.1 — core schema for the Usage Metering & Billing Engine
-- Money is integer micro-USD; pricing constants live in application config, NOT here.
-- Stack: Go + PostgreSQL (deliberate deviation from the brief's Node/Python — see BUILDLOG.md).

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- provides gen_random_uuid()

-- 1. tenants ----------------------------------------------------------------
CREATE TABLE tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL CHECK (length(trim(name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- 2. plans (entitlements only; prices are in app config) --------------------
CREATE TABLE plans (
    id             text PRIMARY KEY,                 -- 'free' | 'pro'
    name           text NOT NULL,
    api_call_limit bigint NOT NULL CHECK (api_call_limit >= 0),
    ai_token_limit bigint NOT NULL CHECK (ai_token_limit >= 0)
);

-- 3. subscriptions (one active plan per tenant; updated by Stripe webhooks) --
CREATE TABLE subscriptions (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    plan_id                text NOT NULL REFERENCES plans(id),
    status                 text NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active','canceled','past_due')),
    stripe_customer_id     text,
    stripe_subscription_id text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_sub_tenant UNIQUE (tenant_id),           -- one active subscription/tenant
    CONSTRAINT uq_sub_stripe UNIQUE (stripe_subscription_id) -- webhook target; NULLs allowed
);
CREATE INDEX idx_sub_stripe_customer ON subscriptions(stripe_customer_id);

-- 4. tenant_api_keys (auth; store a SHA-256 hash, never the raw key) --------
CREATE TABLE tenant_api_keys (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key_hash   text NOT NULL,                          -- sha256(raw key), hex
    key_prefix text NOT NULL,                          -- first chars, for display/debug
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT uq_api_key_hash UNIQUE (key_hash)
);
CREATE INDEX idx_api_keys_tenant ON tenant_api_keys(tenant_id);

-- 5. idempotency_keys (claim + stored response; one row per request) --------
CREATE TABLE idempotency_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    request_hash    text NOT NULL,                      -- hash of NORMALIZED request body
    response_status int,                                -- NULL until the claimer commits
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    CONSTRAINT uq_idem_tenant_key UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT ck_idem_status
        CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599)
);

-- 6. usage_events (immutable, append-only billing ledger; N rows per request)
CREATE TABLE usage_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    idempotency_key_id  uuid NOT NULL REFERENCES idempotency_keys(id) ON DELETE CASCADE,
    type                text NOT NULL CHECK (type IN ('api_call','ai_tokens')),
    quantity            bigint NOT NULL DEFAULT 0 CHECK (quantity >= 0),            -- api_call
    input_tokens        bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),        -- UNCACHED
    cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
    output_tokens       bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_tokens    bigint NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- a row records exactly one usage dimension:
    CONSTRAINT ck_usage_dimension CHECK (
        (type = 'api_call'
            AND input_tokens = 0 AND cached_input_tokens = 0
            AND output_tokens = 0 AND reasoning_tokens = 0)
     OR (type = 'ai_tokens' AND quantity = 0)
    )
);
CREATE INDEX idx_usage_tenant_time ON usage_events(tenant_id, created_at);
CREATE INDEX idx_usage_idem        ON usage_events(idempotency_key_id);

-- 7. usage_counters (fast, race-free quota gate; one row per tenant+period) --
CREATE TABLE usage_counters (
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period         text NOT NULL,                       -- 'YYYY-MM' (UTC calendar month)
    api_calls_used bigint NOT NULL DEFAULT 0 CHECK (api_calls_used >= 0),
    ai_tokens_used bigint NOT NULL DEFAULT 0 CHECK (ai_tokens_used >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, period)
);

-- 8. processed_stripe_events (webhook dedup; used in Phase 3) ---------------
CREATE TABLE processed_stripe_events (
    event_id     text PRIMARY KEY,                      -- Stripe event.id
    tenant_id    uuid REFERENCES tenants(id) ON DELETE SET NULL,
    event_type   text,
    processed_at timestamptz NOT NULL DEFAULT now()
);
