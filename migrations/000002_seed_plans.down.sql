-- Reverse of 000002_seed_plans.up.sql — SAFE rollback.
-- Remove the seeded plans ONLY where no subscription still references them. A plan that a
-- subscription points at is PRESERVED: deleting it would violate subscriptions.plan_id and
-- would strand a live tenant. The seed is re-assertable later (up uses ON CONFLICT DO UPDATE),
-- so preserving a referenced plan here is safe and lossless.
DELETE FROM plans p
WHERE p.id IN ('free', 'pro')
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.plan_id = p.id);
