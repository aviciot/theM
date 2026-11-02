package them

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The user_jwt auth mode uses the access token returned by the-M login as the bearer.
func TestNewClientExposesLoginJWT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/api/v1/auth/login" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"access_token":"tok-123"}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if c.JWT() != "tok-123" {
		t.Errorf("JWT() = %q, want tok-123", c.JWT())
	}
}

func TestNewClientLoginRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	if _, err := NewClient(srv.URL, "u", "bad"); err == nil {
		t.Fatal("expected an error for a rejected login")
	}
}
