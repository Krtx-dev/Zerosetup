//go:build integration

// Package integration runs fixtures through the real engine against a real
// Docker engine. Run with: go test -tags integration ./tests/integration -v
//
// These tests pull images and install dependencies from public registries,
// so they need network access and take minutes.
package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/passport"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/runtime/docker"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/store"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

func service(t *testing.T) *engine.Service {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "db"))
	if err != nil {
		t.Fatal(err)
	}
	s := engine.New(engine.PathsAt(home), st, func(ctx context.Context) (runtime.Backend, error) { return docker.New(ctx) })
	if _, err := s.Backend(context.Background()); err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func fixture(name string) string {
	d, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", name))
	return d
}

func create(t *testing.T, s *engine.Service, name string, ov planner.Overrides) *sessions.Session {
	t.Helper()
	ctx := context.Background()
	req := engine.InspectRequest{Source: fixture(name), Overrides: ov}
	res, err := s.Inspect(ctx, req)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	x, err := s.CreateSession(ctx, req, res)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 3. Execution approval is required.
	if _, err := s.Execute(ctx, x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("execution without approval must be refused, got %v", err)
	}
	if _, err := s.Approve(ctx, x.ID, x.PlanHash); err != nil {
		t.Fatal(err)
	}
	return x
}

func cleanup(t *testing.T, s *engine.Service, id string) {
	t.Helper()
	ctx := context.Background()
	res, err := s.Remove(ctx, id)
	if err != nil || !res.Verified {
		t.Fatalf("cleanup: %v %+v", err, res)
	}
	b, _ := s.Backend(ctx)
	left, err := b.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: id})
	if err != nil || !left.Empty() {
		t.Fatalf("owned resources remain after removal: %+v", left)
	}
}

func TestSupportedFixtures(t *testing.T) {
	cases := []struct {
		name, fixture, body, path string
		ov                        planner.Overrides
	}{
		{name: "vite", body: "/@vite/client"},
		{name: "express", body: "Hello from fixture-express"},
		{name: "fastapi", body: "Swagger UI", path: "/docs"},
		{name: "nextjs", body: "Hello from fixture-nextjs"},
		{name: "vite-pnpm", body: "/@vite/client"},
		{name: "express-yarn", body: "Hello from fixture-express-yarn"},
		{name: "fastapi-uv", body: "Swagger UI", path: "/docs"},
		{name: "flask", body: "Hello from fixture-flask"},
		{name: "django", body: "Hello from fixture-django"},
		{name: "monorepo-npm-workspace", fixture: "monorepo-npm", body: "/src/main.js", ov: planner.Overrides{Workspace: "apps/web"}},
		{name: "vite-production", fixture: "vite", body: "/assets/index-", ov: planner.Overrides{Mode: "production"}},
		{name: "nextjs-production", fixture: "nextjs", body: "Hello from fixture-nextjs", ov: planner.Overrides{Mode: "production"}},
		{name: "express-services", body: "postgres-ok redis-ok", ov: planner.Overrides{Services: []string{"postgres", "redis"}}},
		{name: "express-yarn-berry", body: "Hello from fixture-express-yarn-berry"},
		{name: "monorepo-pnpm-workspace", fixture: "monorepo-pnpm", body: "/src/main.js", ov: planner.Overrides{Workspace: "apps/web"}},
		{name: "monorepo-yarn-workspace", fixture: "monorepo-yarn", body: "/src/main.js", ov: planner.Overrides{Workspace: "apps/web"}},
		{name: "flask-gunicorn-production", fixture: "flask-gunicorn", body: "Hello from fixture-flask-gunicorn", ov: planner.Overrides{Mode: "production"}},
		{name: "django-gunicorn-production", fixture: "django-gunicorn", body: "Hello from fixture-django-gunicorn", ov: planner.Overrides{Mode: "production"}},
	}
	if only := os.Getenv("ZS_ONLY"); only != "" {
		filtered := cases[:0]
		for _, c := range cases {
			if strings.Contains(","+only+",", ","+c.name+",") {
				filtered = append(filtered, c)
			}
		}
		cases = filtered
	}
	s := service(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			start := time.Now()
			fx := c.fixture
			if fx == "" {
				fx = c.name
			}
			x := create(t, s, fx, c.ov)
			defer cleanup(t, s, x.ID)
			x, err := s.Execute(ctx, x.ID, nil)
			if err != nil {
				var logs strings.Builder
				_ = s.Logs(context.Background(), x.ID, "app", false, "80", &logs)
				d, _ := s.Diagnostics(context.Background(), x.ID)
				t.Fatalf("execute: %v\ndiagnostics: %+v\nlogs:\n%s", err, d, logs.String())
			}
			t.Logf("%s ready in %s at %s", c.name, time.Since(start).Round(time.Second), x.URL)
			// 6./7. HTTP readiness passed and the URL serves the app.
			if x.State != sessions.Ready || x.Milestones[sessions.MilestoneHTTPReady].IsZero() {
				t.Fatalf("state %s milestones %v", x.State, x.Milestones)
			}
			resp, err := http.Get(x.URL + c.path + map[bool]string{true: "/", false: ""}[c.path == ""])
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(body), c.body) {
				t.Fatalf("preview body does not contain %q: %.300s", c.body, body)
			}
			// 8. Logs are accessible.
			var logs strings.Builder
			if err := s.Logs(ctx, x.ID, "app", false, "50", &logs); err != nil || logs.Len() == 0 {
				t.Fatalf("logs: %v (%d bytes)", err, logs.Len())
			}
			// 9. Session state is persisted.
			if y, err := s.Store.Get(ctx, x.ID); err != nil || y.State != sessions.Ready || y.URL != x.URL {
				t.Fatalf("persisted session: %v %+v", err, y)
			}
			// 12. Export produces valid metadata.
			b, p, err := s.Passport(ctx, x.ID)
			if err != nil || passport.Validate(p) != nil || p.Environment.BaseImageDigest == "" {
				t.Fatalf("passport: %v", err)
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			if err != nil {
				t.Fatal("passport is not valid JSON")
			}
			if err := passportSchema(t).Validate(doc); err != nil {
				t.Fatalf("passport does not match schemas/passport-1.1.json: %v", err)
			}
			// 10. Stop works.
			if y, err := s.Stop(ctx, x.ID); err != nil || y.State != sessions.Stopped {
				t.Fatalf("stop: %v", err)
			}
			if _, err := http.Get(x.URL); err == nil {
				t.Log("note: port still accepts connections after stop (engine forwarder); verifying container state instead")
			}
			b2, _ := s.Backend(ctx)
			st, err := b2.Inspect(ctx, x.ContainerID)
			if err != nil || st.Running {
				t.Fatalf("container still running after stop: %v", err)
			}
			// 11. Cleanup is verified in the deferred cleanup().
		})
	}
}

func TestFailingFixtures(t *testing.T) {
	s := service(t)
	ctx := context.Background()

	t.Run("broken-port", func(t *testing.T) {
		x := create(t, s, "broken-port", planner.Overrides{})
		defer cleanup(t, s, x.ID)
		start := time.Now()
		x, err := s.Execute(ctx, x.ID, nil)
		if !zerr.HasCode(err, zerr.CodeNotReady) || x.State != sessions.Failed {
			t.Fatalf("want NOT_READY/FAILED, got %v %s", err, x.State)
		}
		if !x.Milestones[sessions.MilestoneHTTPReady].IsZero() {
			t.Fatal("false HTTP_READY")
		}
		d, _ := s.Diagnostics(ctx, x.ID)
		if len(d) == 0 || d[0].ID != "ZS-DIAG-LOOPBACK" {
			t.Fatalf("diagnostics: %+v", d)
		}
		t.Logf("loopback bind diagnosed in %s", time.Since(start).Round(time.Second))
	})

	t.Run("unhealthy", func(t *testing.T) {
		x := create(t, s, "unhealthy", planner.Overrides{ReadyTimeoutSec: 20})
		defer cleanup(t, s, x.ID)
		x, err := s.Execute(ctx, x.ID, nil)
		if !zerr.HasCode(err, zerr.CodeNotReady) || x.State == sessions.Ready {
			t.Fatalf("a server answering only 500 must not be READY: %v %s", err, x.State)
		}
		if x.Milestones[sessions.MilestonePortListening].IsZero() {
			t.Fatal("PORT_LISTENING should have been recorded")
		}
		d, _ := s.Diagnostics(ctx, x.ID)
		if len(d) == 0 || d[0].ID != "ZS-DIAG-HTTP-NOT-READY" {
			t.Fatalf("diagnostics: %+v", d)
		}
	})

	t.Run("missing-start-script", func(t *testing.T) {
		res, err := s.Inspect(ctx, engine.InspectRequest{Source: fixture("missing-start-script")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Plan.Compatibility.Runnable {
			t.Fatal("must not be runnable")
		}
		if _, err := s.CreateSession(ctx, engine.InspectRequest{Source: fixture("missing-start-script")}, res); !zerr.HasCode(err, zerr.CodePolicyViolation) {
			t.Fatalf("non-runnable plans must not become sessions: %v", err)
		}
	})

	t.Run("conflicting-config", func(t *testing.T) {
		res, err := s.Inspect(ctx, engine.InspectRequest{Source: fixture("conflicting-config")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Plan.Compatibility.Runnable || len(res.Plan.Conflicts) == 0 {
			t.Fatalf("conflicting lockfiles must be reported, not silently resolved: %+v", res.Plan.Conflicts)
		}
	})
}

// TestGitHubSource runs a real public repository (Vite's official React
// template) pinned to a full commit SHA.
func TestGitHubSource(t *testing.T) {
	const url = "https://github.com/vitejs/vite/tree/483512809bbdb399d66be59af0f5aeb01ccb7181/packages/create-vite/template-react"
	s := service(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	req := engine.InspectRequest{Source: url}
	res, err := s.Inspect(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Source.Commit != "483512809bbdb399d66be59af0f5aeb01ccb7181" || res.Plan.Source.Subdir != "packages/create-vite/template-react" {
		t.Fatalf("provenance: %+v", res.Plan.Source)
	}
	if again, err := s.Inspect(ctx, req); err != nil || !again.Resolved.CacheHit || again.PlanHash != res.PlanHash {
		t.Fatalf("second inspection must hit the source cache with an identical plan: %v", err)
	}
	x, err := s.CreateSession(ctx, req, res)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, s, x.ID)
	s.Approve(ctx, x.ID, x.PlanHash)
	if x, err = s.Execute(ctx, x.ID, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	resp, err := http.Get(x.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("preview: %v", err)
	}
	resp.Body.Close()
	_, p, err := s.Passport(ctx, x.ID)
	if err != nil || p.Source.Commit != "483512809bbdb399d66be59af0f5aeb01ccb7181" || p.Dependencies.Locked {
		t.Fatalf("passport must record the commit and that dependencies were not locked: %v %+v", err, p.Dependencies)
	}
}

// TestRepairAgainstDocker: a server that binds loopback by default fails
// fast, the proposed repair (HOST=0.0.0.0) is applied to the plan and
// approved, and the re-run becomes READY.
func TestRepairAgainstDocker(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	x := create(t, s, "loopback-default", planner.Overrides{})
	defer cleanup(t, s, x.ID)
	x, err := s.Execute(ctx, x.ID, nil)
	if !zerr.HasCode(err, zerr.CodeNotReady) {
		t.Fatalf("want NOT_READY, got %v", err)
	}
	out, err := s.ApplyRepair(ctx, x.ID, "bind-all-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("repaired plan must need approval: %v", err)
	}
	s.Approve(ctx, x.ID, out.NewHash)
	x, err = s.Execute(ctx, x.ID, nil)
	if err != nil || x.State != sessions.Ready {
		t.Fatalf("after repair: %v", err)
	}
	resp, err := http.Get(x.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "fixture-loopback-default") {
		t.Fatalf("body: %s", body)
	}
}

// TestReplayAgainstDocker exports a passport and replays it in a fresh data
// directory, as a collaborator would.
func TestReplayAgainstDocker(t *testing.T) {
	ctx := context.Background()
	s := service(t)
	x := create(t, s, "express", planner.Overrides{Env: map[string]string{"GREETING": "hi"}})
	x, err := s.Execute(ctx, x.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Passport(ctx, x.ID)
	cleanup(t, s, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := service(t)
	rep, err := other.Replay(ctx, b, engine.ReplayOptions{Source: fixture("express"), CreateSession: true, CheckImages: true})
	if err != nil || !rep.Reproduced || !rep.CanRun || rep.Session == nil {
		t.Fatalf("replay: %v %+v", err, rep)
	}
	defer cleanup(t, other, rep.Session.ID)
	other.Approve(ctx, rep.Session.ID, rep.PlanHash)
	if y, err := other.Execute(ctx, rep.Session.ID, nil); err != nil || y.State != sessions.Ready {
		t.Fatalf("replayed run: %v", err)
	}
}

// TestDaemonRestart is opt-in because it restarts the Docker daemon:
// ZS_DAEMON_RESTART_CMD="colima ssh -- sudo systemctl restart docker".
func TestDaemonRestart(t *testing.T) {
	cmdline := os.Getenv("ZS_DAEMON_RESTART_CMD")
	if cmdline == "" {
		t.Skip("set ZS_DAEMON_RESTART_CMD to run")
	}
	ctx := context.Background()
	s := service(t)
	x := create(t, s, "express", planner.Overrides{})
	defer cleanup(t, s, x.ID)
	x, err := s.Execute(ctx, x.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(cmdline)
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("restart daemon: %v %s", err, out)
	}
	var y *sessions.Session
	for i := 0; i < 60; i++ {
		if y, err = s.Status(ctx, x.ID); err == nil && y.State != sessions.Ready && y.State != sessions.Unknown {
			break
		}
		time.Sleep(time.Second)
	}
	if y == nil || y.State != sessions.Stopped {
		t.Fatalf("after a daemon restart the session must be reconciled to STOPPED, got %v %v", y, err)
	}
	if y, err = s.Restart(ctx, x.ID, nil); err != nil || y.State != sessions.Ready {
		t.Fatalf("restart after daemon restart: %v", err)
	}
}

// passportSchema compiles schemas/passport-1.1.json (with its plan schema).
func passportSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	for _, f := range []string{"plan-1.1.json", "passport-1.1.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "schemas", f))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://zerosetup.dev/schemas/"+f, doc); err != nil {
			t.Fatal(err)
		}
	}
	sch, err := c.Compile("https://zerosetup.dev/schemas/passport-1.1.json")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
