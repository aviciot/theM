#!/bin/bash
# Smoke test for governance slice 1b
set -e

echo "=== Governance Policy Slice 1b Smoke Test ==="
echo

# 1. Login
echo "[1/5] Login..."
curl -s -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' > /tmp/login.json
TOKEN=$(python3 -c "import sys,json; print(json.load(open('/tmp/login.json'))['access_token'])")

if [ -z "$TOKEN" ]; then
  echo "❌ FAIL: Could not get token"
  exit 1
fi
echo "✅ Got token (${#TOKEN} chars)"
echo

# 2. Get tenant policy
echo "[2/5] GET /api/v1/admin/gateway/policy (tenant policy)..."
POLICY_RESP=$(curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/policy)
echo "$POLICY_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print('  allowed_models:', d.get('allowed_models', [])); print('  monthly_budget_usd:', d.get('monthly_budget_usd'))" || echo "$POLICY_RESP"
echo

# 3. Get policy profiles
echo "[3/5] GET /api/v1/admin/gateway/policy-profiles (list)..."
PROFILES_RESP=$(curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/policy-profiles)
echo "$PROFILES_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print('  Count:', len(d)); [print(f'  - {p[\"name\"]} (id={p[\"id\"]})') for p in d[:3]]" || echo "$PROFILES_RESP"
echo

# 4. Check gateway clients
echo "[4/5] GET /api/v1/admin/gateway/clients (check governance_profile_id)..."
CLIENTS_RESP=$(curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/clients)
echo "$CLIENTS_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); c=d[0] if d else {}; print('  First client:'); print('    id:', c.get('id')); print('    governance_profile_id:', c.get('governance_profile_id')); print('    policy_profile_id (legacy):', c.get('policy_profile_id'))" || echo "$CLIENTS_RESP"
echo

# 5. Check DB directly
echo "[5/5] Direct DB check..."
docker exec them-postgres psql -U them -d them -t -c "SELECT COUNT(*) FROM governance_policies;" | xargs echo "  governance_policies rows:"
docker exec them-postgres psql -U them -d them -t -c "SELECT scope_type, COUNT(*) FROM governance_policies GROUP BY scope_type;" | while read line; do echo "    $line"; done
echo

echo "✅ Smoke test complete"
