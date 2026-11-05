-- Revert response_body back to jsonb.
ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE jsonb USING response_body::jsonb;
