package llmgateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aviciot/them/internal/tenantctx"
)

// gwClientKey is the context key for the authenticated gateway client info.
type gwClientKey struct{}

// GatewayClientInfo holds the context injected by GatewayClientMiddleware.
type GatewayClientInfo struct {
	ClientID  string // gateway_clients.id UUID
	TenantID  string // resolved tenant_id from gateway_clients
	TokenHash string // sha256-hex of the bearer token
}

// GatewayClientFromCtx retrieves the GatewayClientInfo stored by GatewayClientMiddleware.
func GatewayClientFromCtx(ctx context.Context) (*GatewayClientInfo, bool) {
	v, ok := ctx.Value(gwClientKey{}).(*GatewayClientInfo)
	return v, ok && v != nil
}

// ClientTenantLookup is the DB interface needed by GatewayClientMiddleware.
// The DAL implements this.
type ClientTenantLookup interface {
	LookupClientTenant(ctx context.Context, tokenHash string) (clientID, tenantID string, err error)
}

// GatewayClientMiddleware authenticates LLM Gateway requests using the bearer
// token in the Authorization header. It:
//   - sha256-hashes the raw bearer value
//   - looks up gateway_clients by token_hash to find tenant_id
//   - sets TenantID in context via tenantctx.WithTenantID
//   - stores GatewayClientInfo in context
//
// This middleware replaces BearerTenantMiddleware for gateway routes — gateway
// clients are not platform users and their tokens are not in them.access_tokens.
func GatewayClientMiddleware(db ClientTenantLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractGatewayBearer(r)
			if !ok {
				writeGatewayMWError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}

			sum := sha256.Sum256([]byte(raw))
			hashHex := fmt.Sprintf("%x", sum)

			clientID, tenantID, err := db.LookupClientTenant(r.Context(), hashHex)
			if err != nil || tenantID == "" {
				writeGatewayMWError(w, http.StatusUnauthorized, "invalid or unknown gateway token")
				return
			}

			info := &GatewayClientInfo{
				ClientID:  clientID,
				TenantID:  tenantID,
				TokenHash: hashHex,
			}
			ctx := context.WithValue(r.Context(), gwClientKey{}, info)
			ctx = tenantctx.WithTenantID(ctx, tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractGatewayBearer(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(auth[7:])
	return token, token != ""
}

func writeGatewayMWError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"type": "unauthorized", "message": msg}})
	_, _ = w.Write(body)
}
