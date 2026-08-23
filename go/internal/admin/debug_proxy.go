package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DebugProxyHandler serves POST /admin/debug-proxy.
//
// It forwards an HTTP request on behalf of the browser (server-side),
// bypassing CORS restrictions that block direct browser-to-third-party calls
// during pipeline debug sessions. Only reachable by authenticated admin users.
//
// Request body:
//
//	{ "method": "GET", "url": "https://...", "headers": {...}, "body": "..." }
//
// Response: the upstream response body is forwarded as-is with its Content-Type.
// On upstream HTTP ≥400 the proxy returns 502 with a plain-text error.
type DebugProxyHandler struct{}

type debugProxyRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

var debugHTTPClient = &http.Client{Timeout: 30 * time.Second}

// allowedSchemes restricts the proxy to HTTPS/HTTP only.
var allowedSchemes = map[string]bool{"https": true, "http": true}

func (h DebugProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req debugProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	// Basic safety: only allow HTTP/HTTPS, reject blank URLs.
	if req.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	scheme := strings.ToLower(strings.SplitN(req.URL, "://", 2)[0])
	if !allowedSchemes[scheme] {
		http.Error(w, fmt.Sprintf("scheme %q not allowed", scheme), http.StatusBadRequest)
		return
	}

	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}

	upstream, err := http.NewRequestWithContext(r.Context(), method, req.URL, bodyReader)
	if err != nil {
		http.Error(w, "could not build upstream request: "+err.Error(), http.StatusBadRequest)
		return
	}
	for k, v := range req.Headers {
		upstream.Header.Set(k, v)
	}

	resp, err := debugHTTPClient.Do(upstream)
	if err != nil {
		http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		http.Error(w, fmt.Sprintf("upstream HTTP %d: %s", resp.StatusCode, string(body)), http.StatusBadGateway)
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body) //nolint:errcheck
}
