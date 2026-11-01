-- db/128_retention_grants.sql
-- Data retention (plan P3) runs through the admin pool (them_admin). It could already delete from every
-- table it cleans except them.gateway_requests (gateway_request_bodies cascades from it).
GRANT DELETE ON them.gateway_requests TO them_admin;

-- schema_migrations stopped being updated after 111; record the hand-applied ones we know about (F-135).
INSERT INTO them.schema_migrations (version, description, applied_at) VALUES
    ('125_gateway_user_id', 'gateway_requests.user_id (applied by hand 2026-11-01)', NOW()),
    ('126_entry_point_required_role_tenant_roles', 'drop the required_role CHECK so tenant role names can be saved (applied 2026-11-01)', NOW()),
    ('128_retention_grants', 'them_admin may delete gateway_requests (retention job)', NOW())
ON CONFLICT DO NOTHING;
