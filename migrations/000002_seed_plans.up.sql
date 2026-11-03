-- Phase 2.1 — reference data: the Free and Pro plans.
-- Plans are entitlements (quota limits). Pricing constants stay in application config.
-- Idempotent: re-running re-asserts the limits without creating duplicates.

INSERT INTO plans (id, name, api_call_limit, ai_token_limit) VALUES
    ('free', 'Free',  1000,  100000),
    ('pro',  'Pro',  10000, 1000000)
ON CONFLICT (id) DO UPDATE
    SET name           = EXCLUDED.name,
        api_call_limit = EXCLUDED.api_call_limit,
        ai_token_limit = EXCLUDED.ai_token_limit;
