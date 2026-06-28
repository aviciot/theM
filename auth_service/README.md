# MCP Authentication Service

Enterprise-grade authentication and authorization service for MCP Gateway with PostgreSQL backend and JWT token management.

## 🚀 Quick Start

### 1. Generate Secrets

**Using Shell Script (Recommended):**
```bash
# Make script executable
chmod +x generate_secrets.sh

# Generate all secrets
./generate_secrets.sh

# Force update without prompts
./generate_secrets.sh --force
```

**Using Python Script:**
```bash
python generate_secrets.py
```

### 2. Start the Service

```bash
# Build and start
docker-compose up -d --build

# Check health
curl http://localhost:8090/health
```

### 3. Access Points

| Service | URL | Description |
|---------|-----|-------------|
| **Auth Service** | http://localhost:8090/ | Direct access |
| **Via Traefik** | http://localhost/auth/ | Through gateway |
| **Health Check** | http://localhost:8090/health | Service health |
| **API Documentation** | http://localhost:8090/docs | Swagger UI |

## 🔐 Authentication Methods

### API Key Authentication
```bash
# Using X-API-Key header
curl -H "X-API-Key: admin_your_api_key_here" http://localhost:8090/users

# Using Authorization header
curl -H "Authorization: ApiKey admin_your_api_key_here" http://localhost:8090/users
```

### JWT Token Authentication
```bash
# 1. Login with API key to get tokens
curl -X POST http://localhost:8090/login \
  -H "Content-Type: application/json" \
  -d '{"api_key": "admin_your_api_key_here"}'

# Response:
# {
#   "access_token": "eyJ0eXAiOiJKV1QiLCJhbGc...",
#   "refresh_token": "eyJ0eXAiOiJKV1QiLCJhbGc...",
#   "token_type": "bearer",
#   "expires_in": 3600
# }

# 2. Use access token
curl -H "Authorization: Bearer eyJ0eXAiOiJKV1QiLCJhbGc..." http://localhost:8090/users
```

## 🎭 Roles & Permissions

### Predefined Roles

| Role | Permissions | Rate Limit | Description |
|------|-------------|------------|-------------|
| **admin** | `*` (all) | 10,000/hr | Full system access |
| **dba** | `mcp_db_performance:*`, `mcp_analytics:read` | 5,000/hr | Database operations |
| **bizops** | `mcp_analytics:*`, `mcp_db_performance:read` | 1,000/hr | Business operations |
| **developer** | `mcp_docker_control:*`, `mcp_db_performance:*` | 2,000/hr | Development access |
| **infoteam** | `mcp_analytics:*`, `mcp_db_performance:read` | 1,000/hr | Information team |
| **qa** | `mcp_docker_control:read`, `mcp_db_performance:read`, `mcp_analytics:read` | 500/hr | Quality assurance |
| **viewer** | `*:read` (read-only) | 100/hr | Read-only access |

### Permission Wildcards

- `*` - All resources and actions
- `mcp_name:*` - All actions on specific MCP  
- `*:read` - Read-only access to all resources
- `mcp_name:action` - Specific action on specific MCP

## 📚 API Endpoints

### Authentication
```bash
POST /validate         # Traefik forwardAuth endpoint
POST /login           # API key → JWT tokens
POST /refresh         # Refresh access token
POST /logout          # Blacklist tokens
```

### User Management
```bash
GET    /users                    # List all users
POST   /users                    # Create user
GET    /users/{id}               # Get user details  
PUT    /users/{id}               # Update user
DELETE /users/{id}               # Delete user
POST   /users/{id}/regenerate-key # Generate new API key
```

### Role Management
```bash
GET    /roles           # List all roles
POST   /roles           # Create role
GET    /roles/{name}    # Get role details
PUT    /roles/{name}    # Update role  
DELETE /roles/{name}    # Delete role
```

### Configuration
```bash
GET    /config          # Get service config
POST   /config/reload   # Reload configuration
PUT    /config/default-role # Set default role
```

### System
```bash
GET /health    # Health check
GET /info      # Service information
GET /metrics   # Prometheus metrics
```

## 🔧 Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | - | PostgreSQL connection string |
| `JWT_SECRET` | - | JWT signing secret (use generator!) |
| `ACCESS_TOKEN_EXPIRY` | 3600 | Access token lifetime (seconds) |
| `REFRESH_TOKEN_EXPIRY` | 604800 | Refresh token lifetime (seconds) |
| `DEFAULT_ROLE` | viewer | Auto-assigned role for unknown API keys |
| `LOG_LEVEL` | INFO | Logging level |
| `PORT` | 8080 | Service port |

### Database Schema

The service uses dedicated PostgreSQL schema `auth_service` with tables:

- **roles** - Role definitions with permissions
- **users** - User accounts with API key hashes
- **user_sessions** - JWT session tracking
- **blacklisted_tokens** - Revoked tokens
- **auth_audit** - Complete audit trail
- **rate_limits** - Rate limiting counters

## 🛡️ Security Features

### Token Security
- **JWT Signing** - HMAC-SHA256 with configurable secret
- **Token Blacklisting** - Revoked tokens stored until natural expiry
- **Refresh Rotation** - New refresh token issued on each refresh
- **Session Tracking** - Database tracking of all active sessions

### Authentication Security
- **API Key Hashing** - SHA256 hashed storage, never plain text
- **Rate Limiting** - Per-user request limits with database tracking
- **Default Role** - Safe fallback for unknown API keys
- **Audit Trail** - Complete logging of all auth events

### Network Security
- **Non-root Container** - Service runs as unprivileged user
- **Health Checks** - Kubernetes/Docker health monitoring
- **Secure Headers** - Security headers in all responses

## 🎯 Traefik Integration

### Middleware Configuration

Add to `traefik/dynamic/middlewares.yml`:

```yaml
# Auth Service Middleware
auth-service:
  forwardAuth:
    address: "http://auth-service:8080/validate"
    authRequestHeaders:
      - "Authorization"
      - "X-API-Key"
      - "X-Forwarded-For"
      - "X-Forwarded-Host"
    authResponseHeaders:
      - "X-User-ID"
      - "X-User-Name"
      - "X-User-Role"
      - "X-User-Permissions"
      - "X-Rate-Limit"
      - "X-Rate-Remaining"

# Secure MCP chains
mcp-stateless-secure:
  chain:
    middlewares:
      - auth-service        # ✅ Authentication first
      - cors-secure
      - security-headers
      - strip-mcp-prefix
      - sse-headers
      - rate-limit-strict
```

### MCP Integration

MCPs receive user context in headers:

```python
# In your MCP server.py
@app.post("/query")
async def query_database(request: Request):
    user_id = request.headers.get("X-User-ID")
    user_role = request.headers.get("X-User-Role")
    permissions = request.headers.get("X-User-Permissions", "").split(",")
    
    # Use user context for business logic
    if "mcp_db_performance:write" not in permissions:
        raise HTTPException(403, "Write access denied")
    
    # Your MCP logic here
    return {"user": user_id, "data": "secure_data"}
```

## 🧪 Testing

### Manual Testing

```bash
# 1. Health check
curl http://localhost:8090/health

# 2. Get admin API key from generated file
cat ADMIN_API_KEY.txt

# 3. Test authentication
curl -H "X-API-Key: admin_your_key_here" http://localhost:8090/users

# 4. Create test user
curl -X POST http://localhost:8090/users \
  -H "X-API-Key: admin_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{"username": "testuser", "name": "Test User", "role": "viewer"}'

# 5. Test JWT flow
curl -X POST http://localhost:8090/login \
  -H "Content-Type: application/json" \
  -d '{"api_key": "admin_your_key_here"}'
```

### Load Testing

```bash
# Install Apache Bench
apt-get install apache2-utils

# Test authentication endpoint
ab -n 1000 -c 10 -H "X-API-Key: your_api_key" http://localhost:8090/validate
```

## 📊 Monitoring

### Health Check
```json
GET /health
{
  "status": "healthy",
  "database": "connected",
  "active_users": 150
}
```

### Metrics Endpoint
```json
GET /metrics
{
  "user_metrics": {
    "total_users": 245,
    "active_users": 198,
    "recent_logins_1h": 45
  },
  "auth_metrics": {
    "auth_success_1h": 1250,
    "auth_failed_1h": 23,
    "rate_limited_1h": 5,
    "active_sessions": 167
  }
}
```

## 🔄 Deployment

### Production Checklist

- [ ] Generate production secrets with `generate_secrets.sh`
- [ ] Set environment variables (never use .env in production)
- [ ] Configure PostgreSQL with auth_service user/schema
- [ ] Configure proper network security
- [ ] Set up monitoring and alerting
- [ ] Configure log aggregation
- [ ] Set up backup procedures
- [ ] Test disaster recovery

### Docker Networks

The service connects to two required external networks:
- `mcp-gateway-net` - For Traefik integration
- `db-net` - For PostgreSQL access

These networks are assumed to already exist.

## 🆘 Troubleshooting

### Common Issues

**Database Connection Failed**
```bash
# Check if PostgreSQL is running
docker ps | grep postgres

# Check network connectivity
docker run --rm --network pg-network postgres:15 pg_isready -h postgres

# Verify auth_service user exists
docker exec -it postgres-container psql -U postgres -d omni -c "\du"
```


**Authentication Not Working**
```bash
# Check if service is healthy
curl http://localhost:8090/health

# Verify API key format
echo "admin_your_key_here" | wc -c  # Should be reasonable length

# Check logs
docker logs mcp-auth-service
```

**Traefik Integration Issues**
```bash
# Check if service is registered with Traefik
curl http://localhost:8080/api/http/services | jq '.[] | select(.name | contains("auth"))'

# Test forwardAuth endpoint directly
curl -H "X-API-Key: your_key" http://localhost:8090/validate
```

### Logs

```bash
# View service logs
docker logs mcp-auth-service -f

# View database logs  
docker logs postgres-container -f

# View Traefik logs
docker logs mcp-gateway-traefik -f
```

## 🤝 Contributing

1. Follow the existing code patterns
2. Add tests for new features
3. Update documentation
4. Test with PostgreSQL
5. Verify Traefik integration works

## 📄 License

Part of the MCP Performance project.
