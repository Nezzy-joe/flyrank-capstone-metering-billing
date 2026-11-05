-- Store the idempotency response as EXACT bytes (text), not jsonb.
-- jsonb re-orders object keys and normalizes whitespace, which makes an idempotent
-- replay semantically-equal but not byte-identical. The response body is an opaque
-- HTTP payload we never query as JSON, so text is the correct type and gives a
-- byte-for-byte replay. The consistency CHECK and the deferred completeness trigger
-- (which only test response_body IS NULL / NOT NULL) are unaffected.
ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE text USING response_body::text;
