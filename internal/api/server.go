// Package api is the local HTTP API used by the dashboard. It binds only to
// 127.0.0.1, requires a per-process bearer token, rejects foreign Host
// headers (DNS rebinding) and foreign Origins on state changes (CSRF), and
// exposes no way to run arbitrary commands.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerosetup/zerosetup/internal/api/webdist"
	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

// Options configures the server.
type Options struct {
	Port  int    // 0 = random free port
	Token string // empty = generate
}

// Server is the dashboard/API server.
type Server struct {
	svc   *engine.Service
	token string
	ln    net.Listener
	port  int
	mux   *http.ServeMux

	baseCtx context.Context
	mu      sync.Mutex
	ops     map[string]context.CancelFunc
}

// New binds the listener (so the URL is known before serving).
func New(svc *engine.Service, o Options) (*Server, error) {
	tok := o.Token
	if tok == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		tok = hex.EncodeToString(b)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.Port)))
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodePortUnavailable, "dashboard", fmt.Sprintf("cannot listen on 127.0.0.1:%d", o.Port)).
			WithSuggestion("Use --port to pick another port, or --port 0 for a random one.")
	}
	s := &Server{svc: svc, token: tok, ln: ln, port: ln.Addr().(*net.TCPAddr).Port, ops: map[string]context.CancelFunc{}}
	s.routes()
	return s, nil
}

// Port returns the bound port.
func (s *Server) Port() int { return s.port }

// Token returns the access token (for tests).
func (s *Server) Token() string { return s.token }

// DashboardURL is the URL to open, with the token in the fragment.
func (s *Server) DashboardURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/#token=%s", s.port, s.token)
}

// Handler exposes the full handler chain (for tests).
func (s *Server) Handler() http.Handler { return s.secure(s.mux) }

// Serve serves until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	s.baseCtx = ctx
	hs := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(s.ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) allowedHost(h string) bool {
	p := strconv.Itoa(s.port)
	return h == "127.0.0.1:"+p || h == "localhost:"+p || h == "[::1]:"+p
}

func (s *Server) allowedOrigin(o string) bool {
	p := strconv.Itoa(s.port)
	return o == "http://127.0.0.1:"+p || o == "http://localhost:"+p
}

// secure applies host, origin, auth and header policies.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"font-src 'self' data:; connect-src 'self'; frame-src http://127.0.0.1:* http://localhost:*; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if !s.allowedHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if o := r.Header.Get("Origin"); o != "" && !s.allowedOrigin(o) {
					writeErr(w, http.StatusForbidden, zerr.New(zerr.CodePolicyViolation, "api", "cross-origin request refused"))
					return
				}
				if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					writeErr(w, http.StatusUnsupportedMediaType, zerr.New(zerr.CodePlanInvalid, "api", "Content-Type must be application/json"))
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			}
			if r.URL.Path != "/api/v1/health" {
				got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
					writeErr(w, http.StatusUnauthorized, zerr.New(zerr.CodePolicyViolation, "api", "missing or invalid access token").
						WithSuggestion("Open the dashboard using the URL printed by `zerosetup dashboard`."))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/health", s.health)
	m.HandleFunc("GET /api/v1/doctor", s.doctor)
	m.HandleFunc("POST /api/v1/inspect", s.inspect)
	m.HandleFunc("POST /api/v1/plans", s.inspect)
	m.HandleFunc("GET /api/v1/plans/{hash}", s.getPlan)
	m.HandleFunc("POST /api/v1/sessions", s.createSession)
	m.HandleFunc("GET /api/v1/sessions", s.listSessions)
	m.HandleFunc("GET /api/v1/sessions/{id}", s.getSession)
	m.HandleFunc("DELETE /api/v1/sessions/{id}", s.removeSession)
	m.HandleFunc("POST /api/v1/sessions/{id}/approve", s.approve)
	m.HandleFunc("POST /api/v1/sessions/{id}/cancel", s.cancel)
	m.HandleFunc("POST /api/v1/sessions/{id}/stop", s.stop)
	m.HandleFunc("POST /api/v1/sessions/{id}/restart", s.restart)
	m.HandleFunc("GET /api/v1/sessions/{id}/logs", s.logs)
	m.HandleFunc("GET /api/v1/sessions/{id}/events", s.events)
	m.HandleFunc("GET /api/v1/sessions/{id}/metrics", s.metrics)
	m.HandleFunc("GET /api/v1/sessions/{id}/diagnostics", s.diagnostics)
	m.HandleFunc("POST /api/v1/sessions/{id}/repairs/{rid}/apply", s.applyRepair)
	m.HandleFunc("POST /api/v1/sessions/{id}/repairs/revert", s.revertRepair)
	m.HandleFunc("GET /api/v1/sessions/{id}/passport", s.passport)
	m.HandleFunc("GET /api/v1/cache", s.cache)
	m.HandleFunc("POST /api/v1/cache/clean", s.cleanCache)
	m.HandleFunc("GET /api/v1/config", s.config)

	static, _ := fs.Sub(webdist.FS, "dist")
	files := http.FileServer(http.FS(static))
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// SPA: unknown non-asset paths serve index.html.
		if r.URL.Path != "/" && !strings.Contains(r.URL.Path[1:], ".") {
			r.URL.Path = "/"
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			// Content-hashed file names: safe to cache forever.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// index.html must be revalidated so upgrades take effect.
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	s.mux = m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// errorBody is the API error contract.
type errorBody struct {
	Code       string `json:"code"`
	Stage      string `json:"stage,omitempty"`
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, err error) {
	ze, ok := zerr.As(err)
	if !ok {
		ze = zerr.Wrap(err, zerr.CodeInternal, "api", "internal error")
	}
	writeJSON(w, status, map[string]any{"error": errorBody{Code: string(ze.Code), Stage: ze.Stage, Summary: ze.Summary,
		Detail: ze.Detail, Suggestion: ze.Suggestion}})
}

func statusFor(err error) int {
	ze, ok := zerr.As(err)
	if !ok {
		return http.StatusInternalServerError
	}
	switch ze.Code {
	case zerr.CodeSessionNotFound, zerr.CodeSourceNotFound:
		return http.StatusNotFound
	case zerr.CodeSourceInvalid, zerr.CodeSourceUnsupported, zerr.CodePlanInvalid, zerr.CodeApprovalMismatch, zerr.CodeApprovalRequired:
		return http.StatusBadRequest
	case zerr.CodeIllegalState, zerr.CodePolicyViolation:
		return http.StatusConflict
	case zerr.CodeDockerUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func fail(w http.ResponseWriter, err error) { writeErr(w, statusFor(err), err) }

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return zerr.Wrap(err, zerr.CodePlanInvalid, "api", "invalid JSON body")
	}
	return nil
}
