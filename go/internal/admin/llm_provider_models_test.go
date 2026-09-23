package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── doModelsFetch ────────────────────────────────────────────────────────────

func TestDoModelsFetch_Success_DecodesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if errMsg := doModelsFetch(req, &out); errMsg != "" {
		t.Fatalf("want no error, got %q", errMsg)
	}
	if len(out.Data) != 2 || out.Data[0].ID != "model-a" {
		t.Errorf("unexpected decode result: %+v", out.Data)
	}
}

func TestDoModelsFetch_NonOK_ReturnsErrorWithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	var out map[string]any
	errMsg := doModelsFetch(req, &out)
	if errMsg == "" {
		t.Fatal("want non-empty error message on 401")
	}
	if !strings.Contains(errMsg, "401") {
		t.Errorf("want error message to include status code, got %q", errMsg)
	}
}

func TestDoModelsFetch_BadJSON_ReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	var out map[string]any
	errMsg := doModelsFetch(req, &out)
	if errMsg == "" {
		t.Fatal("want non-empty error message on malformed JSON")
	}
}

// ── fetchOpenAICompatModels ──────────────────────────────────────────────────

func TestFetchOpenAICompatModels_SortsAndExtractsIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("want Authorization header Bearer sk-test, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"}]}`))
	}))
	defer srv.Close()

	models, errMsg := fetchOpenAICompatModels(context.Background(), "sk-test", srv.URL)
	if errMsg != "" {
		t.Fatalf("want no error, got %q", errMsg)
	}
	if len(models) != 2 || models[0] != "a-model" || models[1] != "z-model" {
		t.Errorf("want sorted [a-model z-model], got %v", models)
	}
}

// ── listAvailableModels dispatch ─────────────────────────────────────────────

func TestListAvailableModels_UnsupportedProviderNoBaseURL_ReturnsError(t *testing.T) {
	models, errMsg := listAvailableModels(context.Background(), "made-up-provider", "key", "")
	if models != nil {
		t.Errorf("want nil models, got %v", models)
	}
	if !strings.Contains(errMsg, "unsupported provider") {
		t.Errorf("want unsupported-provider error, got %q", errMsg)
	}
}

func TestListAvailableModels_UnsupportedProviderWithBaseURL_UsesCompatFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"custom-model"}]}`))
	}))
	defer srv.Close()

	models, errMsg := listAvailableModels(context.Background(), "custom-vllm", "key", srv.URL)
	if errMsg != "" {
		t.Fatalf("want no error, got %q", errMsg)
	}
	if len(models) != 1 || models[0] != "custom-model" {
		t.Errorf("want [custom-model], got %v", models)
	}
}
