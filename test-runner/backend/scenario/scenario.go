package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type Scenario struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TenantSlug  string   `json:"tenant_slug"`
	AppID       string   `json:"app_id"`
	AppSlug     string   `json:"app_slug"`
	EPSlug      string   `json:"ep_slug"`
	AuthMode    string   `json:"auth_mode"` // "token" | "public" | "user_jwt" | "external_jwt"
	// AuthUser/AuthPass are the credentials used to login to the-M and create
	// bearer tokens. Use a tenant admin (e.g. admin@payops.ai) — super-admin
	// is not required. Falls back to global config credentials when empty.
	AuthUser    string   `json:"auth_user,omitempty"`
	AuthPass    string   `json:"auth_pass,omitempty"`
	NUsers      int      `json:"n_users"`
	Messages    []string `json:"messages"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
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
