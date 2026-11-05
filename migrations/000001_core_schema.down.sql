-- Reverse of 000001_core_schema.up.sql (drop in dependency order).
-- The pgcrypto extension is intentionally left in place (other objects may use it).

DROP TABLE IF EXISTS processed_stripe_events;
DROP TABLE IF EXISTS usage_counters;
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS idempotency_keys;   -- drops trg_idem_require_complete with the table
DROP TABLE IF EXISTS tenant_api_keys;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS tenants;

-- the trigger function is a standalone object; drop it explicitly
DROP FUNCTION IF EXISTS idem_require_complete_at_commit();
