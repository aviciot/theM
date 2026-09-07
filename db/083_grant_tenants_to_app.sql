-- them_app (RLS pool) needs SELECT on them.tenants to satisfy the
-- JOIN in listAppQuery (applications.go). Without this, GET /applications/{id}
-- returns 404 for any tenant-scoped request.
GRANT SELECT ON them.tenants TO them_app;
