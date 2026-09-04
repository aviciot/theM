package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/tenantctx"
)

// AgentsHandler handles /api/v1/admin/agents routes.
type AgentsHandler struct {
	// legacySvc is the fallback service when pools is nil (unit tests only).
	// In production pools is always non-nil and openSvc uses TenantTx.
	legacySvc *service.AgentService
	pools     *db.Pools
	cache     CacheInvalidator
	audit     *AuditWriter
	// Action endpoints (Discover, Test, SecurityScan): cross-tenant reads via admin pool.
	legacyDAL *dal.DB
	redis     rueidis.Client
	fernetKey []byte
}

// NewAgentsHandler creates an AgentsHandler.
// legacyDB backs the unit-test fallback (pools=nil) and the action endpoints
// (Discover, Test, SecurityScan). Pass nil redis / empty fernetKey in tests
// that do not exercise those endpoints.
func NewAgentsHandler(legacyDB DBQuerier, pools *db.Pools, cache CacheInvalidator, redis rueidis.Client, fernetKey []byte, audit *AuditWriter) *AgentsHandler {
	d := dal.NewDB(legacyDB)
	return &AgentsHandler{
		legacySvc: service.NewAgentService(d, cache),
		pools:     pools,
		cache:     cache,
		audit:     audit,
		legacyDAL: d,
		redis:     redis,
		fernetKey: fernetKey,
	}
}

func (h *AgentsHandler) openSvc(ctx context.Context, tenantID string) (svc *service.AgentService, commit func(context.Context) error, rollback func(), err error) {
	if h.pools == nil {
		return h.legacySvc, func(_ context.Context) error { return nil }, func() {}, nil
	}
	tenantUUID, uuidErr := uuid.Parse(tenantID)
	if uuidErr != nil {
		return nil, nil, nil, uuidErr
	}
	tx, txErr := h.pools.BeginTenantTx(ctx, tenantUUID)
	if txErr != nil {
		return nil, nil, nil, txErr
	}
	rb := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}
	return service.NewAgentService(dal.NewDBFromTenantQuerier(tx), h.cache), tx.Commit, rb, nil
}

// Routes mounts the agent CRUD + action endpoints.
// The static /agents/discover route is registered before /{id} routes so
// chi's exact-match wins over the parameter pattern.
func (h *AgentsHandler) Routes(r chi.Router) {
	// Static action routes first.
	r.Post("/agents/discover", h.Discover)
	// CRUD routes.
	r.Get("/agents", h.List)
	r.Post("/agents", h.Create)
	r.Get("/agents/{id}", h.Get)
	r.Put("/agents/{id}", h.Update)
	r.Patch("/agents/{id}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/agents/{id}", h.Delete)
	// Per-agent action routes.
	r.Post("/agents/{id}/test", h.Test)
	r.Post("/agents/{id}/security-scan", h.SecurityScan)
}

// ── CRUD handlers ─────────────────────────────────────────────────────────────

// List handles GET /api/v1/admin/agents.
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	agents, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, agents)
}

// Create handles POST /api/v1/admin/agents.
func (h *AgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	input.CreatedBy = claimsUserID(r)
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	id, err := svc.Create(r.Context(), tenantID, input)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "agent.create", EntityType: "agent", EntityID: id, Actor: actorFromRequest(r),
	})
	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/agents/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	a, err := svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Update(r.Context(), tenantID, id, input); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "agent.update", EntityType: "agent", EntityID: id, Actor: actorFromRequest(r),
		Changes: changesOf(input),
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "agent.delete", EntityType: "agent", EntityID: id, Actor: actorFromRequest(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// ── Action handlers ───────────────────────────────────────────────────────────

// discoverRequest is the body for POST /admin/agents/discover.
type discoverRequest struct {
	EndpointURL string `json:"endpoint_url"`
	AuthToken   string `json:"auth_token,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
}

// Discover handles POST /api/v1/admin/agents/discover.
// It fetches the agent card from the given endpoint, parses it, and returns
// suggested metadata. An optional LLM classifier assigns category + icon.
func (h *AgentsHandler) Discover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.EndpointURL == "" {
		writeError(w, http.StatusBadRequest, "endpoint_url is required")
		return
	}

	// Resolve auth token.
	authToken := req.AuthToken
	if authToken == "" && req.AgentID != "" {
		agent, err := h.legacyDAL.GetAgentByID(r.Context(), req.AgentID)
		if err == nil && agent.AuthTokenSet {
			// We need the raw encrypted token. GetAgentByID doesn't return it.
			// Use the dedicated method.
			encrypted, err2 := h.legacyDAL.GetAgentTokenEncrypted(r.Context(), req.AgentID)
			if err2 == nil && encrypted != "" {
				decrypted, err3 := crypto.DecryptStored(h.fernetKey, encrypted)
				if err3 == nil {
					authToken = decrypted
				}
			}
		}
	}

	// Build card URL.
	endpointURL := strings.TrimRight(req.EndpointURL, "/")
	cardURL := endpointURL + "/.well-known/agent-card.json"

	// Fetch agent card.
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, cardURL, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": "build request: " + err.Error()})
		return
	}
	httpReq.Header.Set("A2A-Version", "1.0")
	if authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"detail": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200)),
		})
		return
	}

	cardBytes, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": "read card: " + err.Error()})
		return
	}

	var card map[string]any
	if err := json.Unmarshal(cardBytes, &card); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": "parse card JSON: " + err.Error()})
		return
	}

	// Extract fields from card.
	displayName, _ := card["name"].(string)
	description, _ := card["description"].(string)
	var skills []any
	if s, ok := card["skills"].([]any); ok {
		skills = s
	}

	// Build description with skill details appended.
	var descParts []string
	if description != "" {
		descParts = append(descParts, description)
	}
	for _, s := range skills {
		if sm, ok := s.(map[string]any); ok {
			skillName, _ := sm["name"].(string)
			skillDesc, _ := sm["description"].(string)
			if skillName != "" && skillDesc != "" {
				descParts = append(descParts, skillName+": "+skillDesc)
			}
		}
	}
	fullDescription := strings.Join(descParts, "\n")

	// Extract capabilities.
	var supportsStreaming, supportsPush bool
	if caps, ok := card["capabilities"].(map[string]any); ok {
		supportsStreaming, _ = caps["streaming"].(bool)
		supportsPush, _ = caps["pushNotifications"].(bool)
	}

	// Extract icon (try iconUrl then icon_url).
	iconVal, _ := card["iconUrl"].(string)
	if iconVal == "" {
		iconVal, _ = card["icon_url"].(string)
	}

	// Build normalized skills list.
	normalizedSkills := make([]map[string]any, 0, len(skills))
	for _, s := range skills {
		if sm, ok := s.(map[string]any); ok {
			skillID, _ := sm["id"].(string)
			skillName, _ := sm["name"].(string)
			skillDesc, _ := sm["description"].(string)
			var tags []string
			if t, ok := sm["tags"].([]any); ok {
				for _, tag := range t {
					if ts, ok := tag.(string); ok {
						tags = append(tags, ts)
					}
				}
			}
			if tags == nil {
				tags = []string{}
			}
			normalizedSkills = append(normalizedSkills, map[string]any{
				"id":          skillID,
				"name":        skillName,
				"description": skillDesc,
				"tags":        tags,
			})
		}
	}

	// LLM classifier (best-effort).
	category, classifierIcon := classifyAgent(r.Context(), h.legacyDAL, h.fernetKey, displayName, fullDescription, skills)

	// Use classifier icon only if no icon was found in the card.
	if iconVal == "" && classifierIcon != "" {
		iconVal = classifierIcon
	}

	// Build suggested_slug.
	slug := slugify(displayName)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"suggested_slug":    slug,
		"display_name":      displayName,
		"description":       fullDescription,
		"skills":            normalizedSkills,
		"supports_streaming": supportsStreaming,
		"supports_push":     supportsPush,
		"icon":              iconVal,
		"agent_card":        card,
		"agent_card_url":    cardURL,
		"category":          category,
	})
}

// Test handles POST /api/v1/admin/agents/{id}/test.
// It always returns HTTP 200; the "ok" field inside conveys success/failure.
func (h *AgentsHandler) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	agent, err := svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	_ = commit(r.Context())

	// Resolve auth token (cross-tenant read — uses legacy admin pool).
	var authToken string
	if agent.AuthTokenSet {
		encrypted, err := h.legacyDAL.GetAgentTokenEncrypted(r.Context(), id)
		if err == nil && encrypted != "" {
			decrypted, err := crypto.DecryptStored(h.fernetKey, encrypted)
			if err == nil {
				authToken = decrypted
			}
		}
	}

	cardURL := strings.TrimRight(agent.EndpointURL, "/") + "/.well-known/agent-card.json"

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, cardURL, nil)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"latency_ms": latency,
			"detail":     "build request: " + err.Error(),
		})
		return
	}
	httpReq.Header.Set("A2A-Version", "1.0")
	if authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"latency_ms": latency,
			"detail":     err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"latency_ms": latency,
			"detail":     fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200)),
		})
		return
	}

	// Parse card to get name and skill count.
	cardBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	var card map[string]any
	cardName := agent.DisplayName
	skillCount := 0
	if err := json.Unmarshal(cardBytes, &card); err == nil {
		if n, ok := card["name"].(string); ok && n != "" {
			cardName = n
		}
		if skills, ok := card["skills"].([]any); ok {
			skillCount = len(skills)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"latency_ms": latency,
		"detail":     fmt.Sprintf("Agent card OK — %s · %d skills", cardName, skillCount),
	})
}

// SecurityScan handles POST /api/v1/admin/agents/{id}/security-scan.
// Returns 202 immediately and launches a background goroutine for the scan.
func (h *AgentsHandler) SecurityScan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc2, commit2, rollback2, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback2()
	target, err := svc2.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	_ = commit2(r.Context())

	// Load security scanner agent (platform-global — cross-tenant read via legacy pool).
	scanner, err := h.legacyDAL.GetAgentBySlug(r.Context(), "security_scanner")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"detail": "Security scanner agent not registered",
		})
		return
	}

	// Build payload — copy all fields before launching goroutine.
	skills, _ := target.Skills.([]any)
	payload := scanAgentPayload{
		AgentID:          target.ID,
		Slug:             target.Slug,
		DisplayName:      target.DisplayName,
		Description:      target.Description,
		EndpointURL:      target.EndpointURL,
		AgentCard:        target.AgentCard,
		Skills:           skills,
		SupportsStreaming: target.SupportsStreaming,
		SupportsPush:     target.SupportsPush,
		HasAuthToken:     target.AuthTokenSet,
	}

	// Resolve scanner encrypted token (need raw from DB).
	scannerTokenEncrypted := ""
	if scanner.AuthTokenSet {
		enc, err := h.legacyDAL.GetAgentTokenEncrypted(r.Context(), scanner.ID)
		if err == nil {
			scannerTokenEncrypted = enc
		}
	}

	timeoutSec := float64(scanner.TimeoutSeconds)
	if timeoutSec <= 0 {
		timeoutSec = 120
	}

	jobID := newUUID()

	// Return 202 immediately.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":   jobID,
		"agent_id": id,
	})

	// Launch scan in background.
	if h.redis != nil {
		go runScanJob(
			tenantID,
			id,
			payload,
			scanner.EndpointURL,
			scannerTokenEncrypted,
			h.fernetKey,
			timeoutSec,
			h.redis,
			h.legacyDAL,
			nil,
		)
	}
}

// ── Package helpers ───────────────────────────────────────────────────────────

// newUUID returns a random UUID v4 string.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// slugify converts a display name to a URL-safe slug (max 48 chars).
// Non-alphanumeric characters are replaced with underscores; leading and
// trailing underscores are stripped.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.ToLower(name)
	// Normalize unicode: replace non-ASCII with space.
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	s = nonAlphanumRe.ReplaceAllString(strings.TrimSpace(b.String()), "_")
	s = strings.Trim(s, "_")
	if len(s) > 48 {
		s = s[:48]
		s = strings.TrimRight(s, "_")
	}
	return s
}

// truncate returns s truncated to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
