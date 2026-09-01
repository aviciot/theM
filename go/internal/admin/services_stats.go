package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/aviciot/them/internal/admin/dal"
)

// ServicesStatsHandler serves GET /admin/services/stats.
// Returns a generic envelope with one key per service so new services can be
// added without breaking existing consumers:
//
//	{ "security": { ... }, "future_service": { ... } }
type ServicesStatsHandler struct {
	db     DBQuerier
	logger *slog.Logger
}

func NewServicesStatsHandler(db DBQuerier, logger *slog.Logger) *ServicesStatsHandler {
	return &ServicesStatsHandler{db: db, logger: logger}
}

func (h *ServicesStatsHandler) Routes(r interface{ Get(string, http.HandlerFunc) }) {
	r.Get("/services/stats", h.getStats)
}

func (h *ServicesStatsHandler) getStats(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	var since time.Time
	switch window {
	case "24h":
		since = time.Now().Add(-24 * time.Hour)
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
	default: // "7d" and anything else
		since = time.Now().Add(-7 * 24 * time.Hour)
	}

	secStats, err := dal.GetSecurityScanStats(r.Context(), h.db, since)
	if err != nil {
		h.logger.Error("services stats: security query failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"security": secStats,
	})
}
