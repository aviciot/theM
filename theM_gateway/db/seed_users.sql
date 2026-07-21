-- =============================================================================
-- the-M — Development User Seed
-- OPTIONAL: Apply only on development/staging deployments.
-- DO NOT apply to production deployments.
--
-- Creates:
--   admin / admin123  (super_admin)
--   avi   / avi123    (super_admin)
--
-- Usage:
--   docker exec -i them-postgres psql -U them -d them < db/seed_users.sql
--   OR via linux-db-init.sh with --seed-users flag
-- =============================================================================

-- Ensure roles exist (should already be present from schema_current.sql)
INSERT INTO auth_service.roles (id, name) VALUES
    (1, 'super_admin'),
    (2, 'developer'),
    (3, 'analyst'),
    (4, 'viewer')
ON CONFLICT (id) DO NOTHING;

SELECT setval('auth_service.roles_id_seq', (SELECT MAX(id) FROM auth_service.roles));

-- Users (bcrypt hashed passwords, cost factor 12)
--   admin  → admin123
--   avi    → avi123
INSERT INTO auth_service.users (username, name, email, role_id, password_hash, active)
VALUES
    ('admin', 'Administrator',  'admin@them.local',      1,
     '$2b$12$DZUNNIwrBXjGksKxfkg0fOqAlvNn47G6hXJ6cOMxP1Bpfiw/ZzVSK', true),
    ('avi',   'Avi Cohen',      'avi.cohen@shift4.com',  1,
     '$2b$12$oePlJ/q0ncXcv7pM7S7IY.IytHiFztMCcOa1xteo/VjYStx5HOCq6', true)
ON CONFLICT (username) DO UPDATE SET
    name          = EXCLUDED.name,
    email         = EXCLUDED.email,
    role_id       = EXCLUDED.role_id,
    password_hash = EXCLUDED.password_hash,
    active        = EXCLUDED.active;

SELECT setval('auth_service.users_id_seq', (SELECT MAX(id) FROM auth_service.users));
