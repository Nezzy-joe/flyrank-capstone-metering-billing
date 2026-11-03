-- Reverse of 000001_core_schema.up.sql (drop in dependency order).
-- The pgcrypto extension is intentionally left in place (other objects may use it).

DROP TABLE IF EXISTS processed_stripe_events;
DROP TABLE IF EXISTS usage_counters;
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS tenant_api_keys;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS tenants;
