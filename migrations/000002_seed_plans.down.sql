-- Reverse of 000002_seed_plans.up.sql.
-- Safe to leave plans in place, but for a clean rollback we remove the seeded rows.
DELETE FROM plans WHERE id IN ('free', 'pro');
