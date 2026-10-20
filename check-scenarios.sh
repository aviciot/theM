#!/bin/bash
docker exec them-test-runner-backend wget -qO- http://localhost:8090/scenarios | python3 -c "
import sys, json
data = json.load(sys.stdin)
for sc in data:
    if sc['id'] == '01-gateway-basic-mock':
        print('=== Scenario: 01-gateway-basic-mock ===')
        print(f\"Name: {sc.get('name', 'N/A')}\")
        print(f\"Description: {sc.get('description', 'MISSING!')}\")
        print(f\"Test Result: {sc.get('test_result_ai_analysis', 'MISSING!')}\")
        print(f\"Users: {sc.get('n_users')}\")
        break
"
