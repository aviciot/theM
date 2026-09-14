package scenario

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/them"
	"github.com/google/uuid"
)

// RunSummary is persisted to history after a run completes.
type RunSummary struct {
	RunID        string            `json:"run_id"`
	ScenarioID   string            `json:"scenario_id"`
	ScenarioName string            `json:"scenario_name"`
	NUsers       int               `json:"n_users"`
	Passed       int               `json:"passed"`
	Failed       int               `json:"failed"`
	StartedAt    string            `json:"started_at"`
	EndedAt      string            `json:"ended_at"`
	Results      []them.UserResult `json:"results"`
}

// RunEvent is streamed to connected SSE clients.
type RunEvent struct {
	Type   string          `json:"type"` // "user_update" | "run_complete"
	UserResult *them.UserResult `json:"user_result,omitempty"`
	Summary    *RunSummary      `json:"summary,omitempty"`
}

// Manager tracks in-progress runs and their SSE subscribers.
type Manager struct {
	mu        sync.Mutex
	active    map[string]*activeRun
	histDir   string
}

type activeRun struct {
	cancel   context.CancelFunc
	subs     []chan RunEvent
	subsMu   sync.Mutex
}

func NewManager(dataDir string) *Manager {
	dir := filepath.Join(dataDir, "history")
	os.MkdirAll(dir, 0755)
	return &Manager{active: make(map[string]*activeRun), histDir: dir}
}

// clientForScenario builds a them.Client using the scenario's own credentials
// when set, falling back to the global config credentials.
func clientForScenario(sc Scenario) (*them.Client, error) {
	cfg := config.Get()
	user := sc.AuthUser
	pass := sc.AuthPass
	if user == "" {
		user = cfg.AdminUser
	}
	if pass == "" {
		pass = cfg.AdminPass
	}
	return them.NewClient(cfg.ThemURL, user, pass)
}

// Start launches a parallel scenario run, returns the run ID immediately.
func (m *Manager) Start(sc Scenario) (string, error) {
	client, err := clientForScenario(sc)
	if err != nil {
		return "", fmt.Errorf("login to the-M: %w", err)
	}
	runID := uuid.New().String()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	ar := &activeRun{cancel: cancel}
	m.mu.Lock()
	m.active[runID] = ar
	m.mu.Unlock()

	go m.execute(ctx, runID, ar, sc, client)
	return runID, nil
}


func (m *Manager) execute(ctx context.Context, runID string, ar *activeRun, sc Scenario, client *them.Client) {
	log := slog.With("run_id", runID, "scenario", sc.Name, "n_users", sc.NUsers)
	log.Info("run: started", "app", sc.AppSlug, "ep", sc.EPSlug, "tenant", sc.TenantSlug, "auth_mode", sc.AuthMode)

	defer func() {
		ar.cancel()
		m.mu.Lock()
		delete(m.active, runID)
		m.mu.Unlock()
	}()

	startedAt := time.Now().UTC().Format(time.RFC3339)
	summary := RunSummary{
		RunID:        runID,
		ScenarioID:   sc.ID,
		ScenarioName: sc.Name,
		NUsers:       sc.NUsers,
		StartedAt:    startedAt,
		Results:      make([]them.UserResult, sc.NUsers),
	}

	// Channel receives updates from all virtual users.
	updates := make(chan them.UserResult, sc.NUsers*10)
	var wg sync.WaitGroup

	for i := 0; i < sc.NUsers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			var bearerToken, tokenID string
			switch sc.AuthMode {
			case "token":
				tok, err := client.CreateToken(sc.AppID, sc.TenantID, fmt.Sprintf("tr-%s-u%d", runID[:8], idx))
				if err != nil {
					updates <- them.UserResult{UserIndex: idx, Status: "failed", Error: fmt.Sprintf("create token: %v", err)}
					return
				}
				bearerToken = tok.Token
				tokenID = tok.ID
				defer client.DeleteToken(tokenID)

			case "external_jwt":
				if len(sc.KeycloakUsers) == 0 {
					updates <- them.UserResult{UserIndex: idx, Status: "failed", Error: "external_jwt: no keycloak_users configured"}
					return
				}
				ku := sc.KeycloakUsers[idx%len(sc.KeycloakUsers)]
				jwt, err := fetchKeycloakJWT(sc.KeycloakURL, sc.KeycloakRealm, sc.KeycloakClientID, sc.KeycloakClientSecret, ku.Username, ku.Password)
				if err != nil {
					updates <- them.UserResult{UserIndex: idx, Status: "failed", Error: fmt.Sprintf("keycloak login (%s): %v", ku.Username, err)}
					return
				}
				bearerToken = jwt
			}

			themURL := client.BaseURL()
			if sc.EPType == "a2a" {
				them.RunUserA2A(ctx, themURL, sc.TenantSlug, sc.AppSlug, sc.EPSlug, bearerToken, idx, sc.Messages, updates)
			} else {
				them.RunUser(ctx, themURL, sc.TenantSlug, sc.AppSlug, sc.EPSlug, bearerToken, idx, sc.Messages, updates)
			}
		}(i)
	}

	// Close updates when all users finish.
	go func() {
		wg.Wait()
		close(updates)
	}()

	// Fan-out updates to SSE subscribers.
	for ur := range updates {
		summary.Results[ur.UserIndex] = ur
		ev := RunEvent{Type: "user_update", UserResult: &ur}
		ar.broadcast(ev)
	}

	// Tally results.
	for _, r := range summary.Results {
		if r.Status == "passed" {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}
	summary.EndedAt = time.Now().UTC().Format(time.RFC3339)

	// Persist to history.
	m.saveHistory(summary)

	log.Info("run: complete", "passed", summary.Passed, "failed", summary.Failed, "duration", summary.EndedAt)

	// Broadcast completion.
	ar.broadcast(RunEvent{Type: "run_complete", Summary: &summary})
}

func (ar *activeRun) broadcast(ev RunEvent) {
	ar.subsMu.Lock()
	defer ar.subsMu.Unlock()
	for _, ch := range ar.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe registers a new SSE channel for the given run.
// Returns nil if the run is not active (already completed).
func (m *Manager) Subscribe(runID string) (<-chan RunEvent, func(), bool) {
	m.mu.Lock()
	ar, ok := m.active[runID]
	m.mu.Unlock()
	if !ok {
		return nil, nil, false
	}

	ch := make(chan RunEvent, 50)
	ar.subsMu.Lock()
	ar.subs = append(ar.subs, ch)
	ar.subsMu.Unlock()

	unsub := func() {
		ar.subsMu.Lock()
		defer ar.subsMu.Unlock()
		for i, c := range ar.subs {
			if c == ch {
				ar.subs = append(ar.subs[:i], ar.subs[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, unsub, true
}

func (m *Manager) Cancel(runID string) {
	m.mu.Lock()
	ar, ok := m.active[runID]
	m.mu.Unlock()
	if ok {
		ar.cancel()
	}
}

func (m *Manager) saveHistory(s RunSummary) {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(m.histDir, s.RunID+".json"), b, 0644)
}

func (m *Manager) ListHistory() ([]RunSummary, error) {
	entries, _ := os.ReadDir(m.histDir)
	var out []RunSummary
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(m.histDir, e.Name()))
		var s RunSummary
		if json.Unmarshal(b, &s) == nil {
			s.Results = nil // strip detail from list view
			out = append(out, s)
		}
	}
	if out == nil {
		out = []RunSummary{}
	}
	return out, nil
}

func (m *Manager) GetHistory(runID string) (*RunSummary, error) {
	b, err := os.ReadFile(filepath.Join(m.histDir, runID+".json"))
	if err != nil {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	var s RunSummary
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *Manager) DeleteHistory(runID string) error {
	return os.Remove(filepath.Join(m.histDir, runID+".json"))
}

// fetchKeycloakJWT obtains an access token from Keycloak using the resource-owner
// password grant. Used by the external_jwt auth mode to simulate bank end-users.
func fetchKeycloakJWT(baseURL, realm, clientID, clientSecret, username, password string) (string, error) {
	tokenURL := strings.TrimRight(baseURL, "/") + "/realms/" + realm + "/protocol/openid-connect/token"
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"username":      {username},
		"password":      {password},
		"scope":         {"openid"},
	}
	resp, err := http.PostForm(tokenURL, form)
	if err != nil {
		return "", fmt.Errorf("keycloak token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("keycloak token: HTTP %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("keycloak decode: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("keycloak: %s — %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}
