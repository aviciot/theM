package them

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	jwt     string
	http    *http.Client
}

func NewClient(baseURL, adminUser, adminPass string) (*Client, error) {
	c := &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
	if err := c.login(adminUser, adminPass); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) login(user, pass string) error {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := c.http.Post(c.baseURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("login: status %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("login decode: %w", err)
	}
	c.jwt = result.AccessToken
	return nil
}

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest("GET", c.baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(path string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(rb))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) delete(path string) error {
	req, _ := http.NewRequest("DELETE", c.baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE %s: status %d", path, resp.StatusCode)
	}
	return nil
}

// Tenant is a minimal tenant record.
type Tenant struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
}

// App is a minimal application record.
type App struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// EP is a minimal entry point record.
type EP struct {
	ID               string         `json:"id"`
	Slug             string         `json:"slug"`
	EntryPointType   string         `json:"entry_point_type"`
	AccessPolicy     map[string]any `json:"access_policy"`
	AllowedPrincipals string        `json:"allowed_principals"`
	Enabled          bool           `json:"enabled"`
}

// Token is a created bearer token.
type Token struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

func (c *Client) ListTenants() ([]Tenant, error) {
	var result []Tenant
	err := c.get("/api/v1/admin/tenants", &result)
	return result, err
}

func (c *Client) ListApps(tenantSlug string) ([]App, error) {
	var result []App
	err := c.get("/api/v1/admin/tenants/"+tenantSlug+"/applications", &result)
	return result, err
}

func (c *Client) ListEPs(appID string) ([]EP, error) {
	var result []EP
	err := c.get("/api/v1/admin/applications/"+appID+"/entry-points", &result)
	return result, err
}

func (c *Client) CreateToken(appID, label string) (*Token, error) {
	var result Token
	err := c.post("/api/v1/admin/tokens", map[string]any{
		"application_id": appID,
		"label":          label,
		"expires_in":     3600,
	}, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteToken(tokenID string) error {
	return c.delete("/api/v1/admin/tokens/" + tokenID)
}

func (c *Client) BaseURL() string { return c.baseURL }
