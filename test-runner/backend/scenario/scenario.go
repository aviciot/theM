package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// KeycloakUser is a single virtual user credential for external_jwt auth mode.
type KeycloakUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Scenario struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	TenantSlug string   `json:"tenant_slug"`
	TenantID   string   `json:"tenant_id,omitempty"`
	AppID      string   `json:"app_id"`
	AppSlug    string   `json:"app_slug"`
	EPSlug     string   `json:"ep_slug"`
	EPType     string   `json:"ep_type,omitempty"` // "websocket" | "sse" | "a2a" — from catalog
	AuthMode   string   `json:"auth_mode"`         // "token" | "public" | "user_jwt" | "external_jwt"
	// AuthUser/AuthPass: the-M admin credentials for token creation (token mode).
	// Falls back to global config credentials when empty.
	AuthUser string `json:"auth_user,omitempty"`
	AuthPass string `json:"auth_pass,omitempty"`
	// Keycloak config — used when auth_mode == "external_jwt".
	// The runner fetches a JWT per virtual user from Keycloak and uses it as the
	// bearer token. KeycloakUsers cycles: user[i % len] is assigned to virtual user i.
	KeycloakURL          string         `json:"keycloak_url,omitempty"`           // e.g. http://them-traefik:8088/auth/keycloak
	KeycloakRealm        string         `json:"keycloak_realm,omitempty"`         // e.g. payops_ai
	KeycloakClientID     string         `json:"keycloak_client_id,omitempty"`     // e.g. them-m
	KeycloakClientSecret string         `json:"keycloak_client_secret,omitempty"` // e.g. them-m-secret
	KeycloakUsers        []KeycloakUser `json:"keycloak_users,omitempty"`         // virtual user credentials
	NUsers               int            `json:"n_users"`
	Messages             []string       `json:"messages"`
	CreatedAt            string         `json:"created_at"`
	UpdatedAt            string         `json:"updated_at"`
}

type Store struct {
	dir string
}

func NewStore(dataDir string) *Store {
	dir := filepath.Join(dataDir, "scenarios")
	os.MkdirAll(dir, 0755)
	return &Store{dir: dir}
}

func (s *Store) List() ([]Scenario, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return []Scenario{}, nil
	}
	var out []Scenario
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sc Scenario
		if json.Unmarshal(b, &sc) == nil {
			out = append(out, sc)
		}
	}
	if out == nil {
		out = []Scenario{}
	}
	return out, nil
}

func (s *Store) Get(id string) (*Scenario, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("scenario %s not found", id)
	}
	var sc Scenario
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

func (s *Store) Create(sc Scenario) (Scenario, error) {
	sc.ID = uuid.New().String()
	sc.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	sc.UpdatedAt = sc.CreatedAt
	return sc, s.write(sc)
}

func (s *Store) Update(id string, sc Scenario) (Scenario, error) {
	sc.ID = id
	sc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return sc, s.write(sc)
}

func (s *Store) Delete(id string) error {
	return os.Remove(s.path(id))
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) write(sc Scenario) error {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(sc.ID), b, 0644)
}
