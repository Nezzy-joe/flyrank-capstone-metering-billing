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

-- 3. subscriptions ----------------------------------------------------------
-- CURRENT-SUBSCRIPTION MODEL: exactly ONE row per tenant (uq_sub_tenant), holding the
-- tenant's present plan + status regardless of value ('active'|'canceled'|'past_due').
-- This mirrors current state, not subscription history. Stripe webhooks UPDATE this row.
-- The default Free subscription is created TRANSACTIONALLY at tenant registration — that
-- is application logic (a tenant + its Free subscription committed together), not enforced
-- by this migration (a DB-level "every tenant has a subscription" rule would need a
-- circular/deferred constraint we deliberately avoid).
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
    CONSTRAINT uq_sub_tenant UNIQUE (tenant_id),            -- one subscription row per tenant
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
    -- composite-unique so usage_events can reference (id, tenant_id) and pin the tenant:
    CONSTRAINT uq_idem_id_tenant UNIQUE (id, tenant_id),
    CONSTRAINT ck_idem_status
        CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599),
    -- response integrity: the record is either fully unpopulated (transient, mid-txn,
    -- never visible to other txns) or fully populated (status + body + completed_at):
    CONSTRAINT ck_idem_response_consistency CHECK (
        (response_status IS NULL     AND response_body IS NULL     AND completed_at IS NULL)
     OR (response_status IS NOT NULL AND response_body IS NOT NULL AND completed_at IS NOT NULL)
    )
);

-- 6. usage_events (immutable, append-only billing ledger; N rows per request)
-- Tenant isolation is STRUCTURAL: the composite FK forces a usage event's tenant_id to
-- equal the tenant_id of its idempotency record — a row cannot be attributed to the wrong
-- tenant. ON DELETE RESTRICT on both FKs protects history: deleting a tenant or an
-- idempotency record is blocked while usage events reference it (the ledger is append-only).
CREATE TABLE usage_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL,
    idempotency_key_id  uuid NOT NULL,
    type                text NOT NULL CHECK (type IN ('api_call','ai_tokens')),
    quantity            bigint NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    input_tokens        bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),        -- UNCACHED
    cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
    output_tokens       bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_tokens    bigint NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- exactly one dimension per row; an api_call is always quantity = 1:
    CONSTRAINT ck_usage_dimension CHECK (
        (type = 'api_call'
            AND quantity = 1
            AND input_tokens = 0 AND cached_input_tokens = 0
            AND output_tokens = 0 AND reasoning_tokens = 0)
     OR (type = 'ai_tokens' AND quantity = 0)
    ),
    -- at most one api_call event and one ai_tokens event per request:
    CONSTRAINT uq_usage_request_dimension UNIQUE (idempotency_key_id, type),
    -- tenant_id ties to tenants, and is pinned to the idempotency record's tenant:
    CONSTRAINT fk_usage_tenant
        FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT,
    CONSTRAINT fk_usage_idem
        FOREIGN KEY (idempotency_key_id, tenant_id)
        REFERENCES idempotency_keys(id, tenant_id) ON DELETE RESTRICT
);
CREATE INDEX idx_usage_tenant_time ON usage_events(tenant_id, created_at);

-- 7. usage_counters (fast, race-free quota gate; one row per tenant+period) --
CREATE TABLE usage_counters (
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period         text NOT NULL,                       -- 'YYYY-MM' (UTC calendar month)
    api_calls_used bigint NOT NULL DEFAULT 0 CHECK (api_calls_used >= 0),
    ai_tokens_used bigint NOT NULL DEFAULT 0 CHECK (ai_tokens_used >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, period),
    -- period must be a real YYYY-MM with month 01..12:
    CONSTRAINT ck_period_format CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$')
);

-- 8. processed_stripe_events (webhook dedup; used in Phase 3) ---------------
CREATE TABLE processed_stripe_events (
    event_id     text PRIMARY KEY,                      -- Stripe event.id
    tenant_id    uuid REFERENCES tenants(id) ON DELETE SET NULL,
    event_type   text,
    processed_at timestamptz NOT NULL DEFAULT now()
);
