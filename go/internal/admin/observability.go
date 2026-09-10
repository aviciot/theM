package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// ObservabilityHandler serves observability endpoints for the super-admin view.
//
// The handler uses rlsPools.Admin (BYPASSRLS) to execute cross-tenant queries.
// Protected by RequireSuperAdmin; no AdminTenantMiddleware is applied.
type ObservabilityHandler struct {
	// adminDB is a DBQuerier backed by the Admin (BYPASSRLS) pool.
	adminDB DBQuerier
	// redis is used for live-today metrics (HGETALL + PFCOUNT). May be nil.
	redis rueidis.Client
}

// NewObservabilityHandler creates an ObservabilityHandler backed by the Admin pool.
func NewObservabilityHandler(pools *db.Pools, redis rueidis.Client) *ObservabilityHandler {
	return &ObservabilityHandler{adminDB: NewPgxQuerier(pools.Admin), redis: redis}
}

// NewObservabilityHandlerForTest creates an ObservabilityHandler with an
// injected DBQuerier and no Redis client. Used only in unit tests.
func NewObservabilityHandlerForTest(q DBQuerier) *ObservabilityHandler {
	return &ObservabilityHandler{adminDB: q}
}

// Routes mounts the observability endpoints on r.
func (h *ObservabilityHandler) Routes(r interface {
	Get(string, http.HandlerFunc)
}) {
	r.Get("/observability/summary", h.Summary)
	r.Get("/observability/tenant/{id}/apps", h.AppBreakdown)
}

// Summary handles GET /api/v1/admin/observability/summary.
func (h *ObservabilityHandler) Summary(w http.ResponseWriter, r *http.Request) {
	rows, err := dal.ListObservabilitySummary(r.Context(), h.adminDB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// AppBreakdown handles GET /api/v1/admin/observability/tenant/{id}/apps.
// Returns per-application stats: 30d from DB merged with live-today from Redis.
func (h *ObservabilityHandler) AppBreakdown(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}

	rows, err := dal.ListAppObservabilitySummary(r.Context(), h.adminDB, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Merge Redis live-today numbers into each row. Errors from Redis are
	// non-fatal — rows are returned with is_live=false when Redis is unavailable.
	if h.redis != nil {
		today := time.Now().UTC().Format("2006-01-02")
		hllKey := fmt.Sprintf("them:metrics:%s:users:%s", tenantID, today)
		activeToday := h.pfcountKey(r.Context(), hllKey)

		for i := range rows {
			appID := rows[i].ApplicationID
			hashKey := fmt.Sprintf("them:metrics:%s:app:%s:%s", tenantID, appID, today)
			fields := h.hgetallKey(r.Context(), hashKey)
			if len(fields) > 0 {
				rows[i].RunsToday = parseInt64(fields["runs"])
				rows[i].TokensInToday = parseInt64(fields["tokens_in"])
				rows[i].TokensOutToday = parseInt64(fields["tokens_out"])
				rows[i].MCPCallsToday = parseInt64(fields["mcp_calls"])
				rows[i].IsLive = true
			}
			rows[i].ActiveUsersToday = activeToday
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

func (h *ObservabilityHandler) hgetallKey(ctx context.Context, key string) map[string]string {
	cmd := h.redis.B().Hgetall().Key(key).Build()
	res := h.redis.Do(ctx, cmd)
	if res.Error() != nil {
		return nil
	}
	m, err := res.AsStrMap()
	if err != nil {
		return nil
	}
	return m
}

func (h *ObservabilityHandler) pfcountKey(ctx context.Context, key string) int64 {
	cmd := h.redis.B().Pfcount().Key(key).Build()
	res := h.redis.Do(ctx, cmd)
	if res.Error() != nil {
		return 0
	}
	v, err := res.AsInt64()
	if err != nil {
		return 0
	}
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
