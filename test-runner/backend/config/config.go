package config

import (
	"encoding/json"
	"os"
	"sync"
)

type Config struct {
	ThemURL    string `json:"them_url"`
	AdminUser  string `json:"admin_user"`
	AdminPass  string `json:"admin_pass"`
	DataDir    string `json:"-"`
	Port       string `json:"-"`
	BasicUser  string `json:"-"`
	BasicPass  string `json:"-"`
	DBURL      string `json:"-"` // postgres DSN — env only, never persisted
}

var (
	mu      sync.RWMutex
	current *Config
)

func Load(dataDir string) (*Config, error) {
	cfg := &Config{
		ThemURL:   env("THEM_URL", "http://localhost:8088"),
		AdminUser: env("THEM_ADMIN_USER", "admin"),
		AdminPass: env("THEM_ADMIN_PASS", ""),
		DataDir:   dataDir,
		Port:      env("PORT", "8090"),
		BasicUser: env("TR_BASIC_AUTH_USER", ""),
		BasicPass: env("TR_BASIC_AUTH_PASS", ""),
		DBURL:     env("DB_URL", ""),
	}

	// Override with persisted config if it exists.
	path := dataDir + "/config.json"
	if b, err := os.ReadFile(path); err == nil {
		var saved Config
		if json.Unmarshal(b, &saved) == nil {
			if saved.ThemURL != "" {
				cfg.ThemURL = saved.ThemURL
			}
			if saved.AdminUser != "" {
				cfg.AdminUser = saved.AdminUser
			}
			if saved.AdminPass != "" {
				cfg.AdminPass = saved.AdminPass
			}
		}
	}

	mu.Lock()
	current = cfg
	mu.Unlock()
	return cfg, nil
}

func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func Save(themURL, adminUser, adminPass string) error {
	mu.Lock()
	defer mu.Unlock()
	current.ThemURL = themURL
	current.AdminUser = adminUser
	if adminPass != "" {
		current.AdminPass = adminPass
	}
	path := current.DataDir + "/config.json"
	b, _ := json.MarshalIndent(&Config{
		ThemURL:   current.ThemURL,
		AdminUser: current.AdminUser,
		AdminPass: current.AdminPass,
	}, "", "  ")
	return os.WriteFile(path, b, 0600)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
