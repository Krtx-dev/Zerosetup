package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := engine.New(engine.PathsAt(home), st, func(context.Context) (runtime.Backend, error) { return nil, errors.New("no docker in this test") })
	s, err := New(svc, Options{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ln.Close(); svc.Close() })
	return s
}

func do(s *Server, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:" + strconv.Itoa(s.Port())
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestAPISecurity(t *testing.T) {
	s := newTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + s.Token()}
	fixture, _ := filepath.Abs("../../fixtures/express")
	body := `{"source":` + strconv.Quote(fixture) + `}`

	if w := do(s, "GET", "/api/v1/health", "", nil); w.Code != 200 {
		t.Fatalf("health: %d", w.Code)
	}
	if w := do(s, "GET", "/api/v1/sessions", "", nil); w.Code != 401 {
		t.Fatalf("missing token: %d", w.Code)
	}
	if w := do(s, "GET", "/api/v1/sessions", "", map[string]string{"Authorization": "Bearer wrong"}); w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	// DNS rebinding: an attacker domain resolving to 127.0.0.1.
	h := map[string]string{"Authorization": auth["Authorization"], "Host": "evil.example:" + strconv.Itoa(s.Port())}
	if w := do(s, "GET", "/api/v1/sessions", "", h); w.Code != 403 {
		t.Fatalf("foreign Host must be refused: %d", w.Code)
	}
	// CSRF: a foreign origin cannot change state even with a token.
	h = map[string]string{"Authorization": auth["Authorization"], "Origin": "https://evil.example"}
	if w := do(s, "POST", "/api/v1/inspect", body, h); w.Code != 403 {
		t.Fatalf("foreign Origin must be refused: %d", w.Code)
	}
	// Form-encoded bodies (simple CORS requests) are refused.
	req := httptest.NewRequest("POST", "/api/v1/inspect", strings.NewReader("source=x"))
	req.Host = "127.0.0.1:" + strconv.Itoa(s.Port())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", auth["Authorization"])
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 415 {
		t.Fatalf("non-JSON body must be refused: %d", w.Code)
	}
	// No way to smuggle commands: unknown fields are rejected.
	if w := do(s, "POST", "/api/v1/inspect", `{"source":"x","command":"rm -rf /"}`, auth); w.Code != 400 {
		t.Fatalf("unknown fields must be rejected: %d %s", w.Code, w.Body)
	}
	// Invalid session IDs are rejected before touching storage.
	if w := do(s, "GET", "/api/v1/sessions/..%2f..%2fetc", "", auth); w.Code != 404 {
		t.Fatalf("invalid id: %d", w.Code)
	}
	// Legitimate same-origin flow works without Docker for inspection.
	h = map[string]string{"Authorization": auth["Authorization"], "Origin": "http://127.0.0.1:" + strconv.Itoa(s.Port())}
	w = do(s, "POST", "/api/v1/inspect", body, h)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Express"`) {
		t.Fatalf("inspect: %d %s", w.Code, w.Body)
	}
	// Creating a session works; executing reports Docker unavailability honestly.
	w = do(s, "POST", "/api/v1/sessions", body, auth)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/v1/sessions/zs00000000/approve", `{"planHash":"x"}`, auth); w.Code != 404 {
		t.Fatalf("approve unknown: %d", w.Code)
	}
	// Security headers on static content.
	w = do(s, "GET", "/", "", nil)
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.Contains(csp, "frame-ancestors 'none'") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("static headers: %d %q", w.Code, csp)
	}
}

func TestBindsLoopbackOnly(t *testing.T) {
	s := newTestServer(t)
	if a := s.ln.Addr().String(); !strings.HasPrefix(a, "127.0.0.1:") {
		t.Fatalf("listener bound to %s", a)
	}
	if !strings.Contains(s.DashboardURL(), "#token=") {
		t.Fatal("token must be delivered in the URL fragment")
	}
	_ = http.StatusOK
}
