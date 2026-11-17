package diagnostics

import (
	"testing"

	"github.com/zerosetup/zerosetup/internal/readiness"
)

func ids(ds []Diagnostic) map[string]Diagnostic {
	m := map[string]Diagnostic{}
	for _, d := range ds {
		m[d.ID] = d
	}
	return m
}

func TestStructuralDiagnostics(t *testing.T) {
	loop := Analyze(Input{Stage: "verify", ExpectedPort: 3000, Listeners: []readiness.Listener{{Addr: "127.0.0.1", Port: 3000, Loopback: true}}})
	d, ok := ids(loop)["ZS-DIAG-LOOPBACK"]
	if !ok || d.Repair == nil || d.Repair.Changes[0].Key != "HOST" || d.Confidence != "high" {
		t.Fatalf("loopback: %+v", loop)
	}
	mm := Analyze(Input{Stage: "verify", ExpectedPort: 3000, TimedOut: true, Listeners: []readiness.Listener{{Addr: "0.0.0.0", Port: 8080}}})
	if d := ids(mm)["ZS-DIAG-PORT-MISMATCH"]; d.Repair == nil || d.Repair.Changes[0].To != "8080" {
		t.Fatalf("mismatch: %+v", mm)
	}
	oom := Analyze(Input{Stage: "verify", Exited: true, OOMKilled: true, MemoryMB: 2048})
	if d := ids(oom)["ZS-DIAG-OOM"]; d.Repair == nil || d.Repair.Changes[0].To != "4096" {
		t.Fatalf("oom: %+v", oom)
	}
	if _, ok := ids(Analyze(Input{Stage: "verify", Exited: true, ExitCode: 127}))["ZS-DIAG-NOT-FOUND"]; !ok {
		t.Fatal("exit 127 must be diagnosed as a missing executable")
	}
	nl := Analyze(Input{Stage: "verify", ExpectedPort: 3000, TimedOut: true, TimeoutSec: 60})
	if _, ok := ids(nl)["ZS-DIAG-NOT-LISTENING"]; !ok {
		t.Fatalf("not listening: %+v", nl)
	}
	http := Analyze(Input{Stage: "verify", ExpectedPort: 3000, TimedOut: true, LastHTTPStatus: 500, ReadinessPath: "/",
		Listeners: []readiness.Listener{{Addr: "::", Port: 3000}}})
	if _, ok := ids(http)["ZS-DIAG-HTTP-NOT-READY"]; !ok {
		t.Fatalf("http not ready: %+v", http)
	}
}

func TestLogDiagnostics(t *testing.T) {
	cases := map[string]string{
		"npm error `npm ci` can only install packages when your package.json and package-lock.json or npm-shrinkwrap.json are in sync.": "ZS-DIAG-LOCKFILE",
		`npm error Missing script: "dev"`:                           "ZS-DIAG-MISSING-SCRIPT",
		"Error: listen EADDRINUSE: address already in use :::3000":  "ZS-DIAG-EADDRINUSE",
		"Error: Cannot find module 'dotenv'":                        "ZS-DIAG-MODULE-NOT-FOUND",
		"ModuleNotFoundError: No module named 'sqlalchemy'":         "ZS-DIAG-MODULE-NOT-FOUND",
		"Error: Missing required environment variable DATABASE_URL": "ZS-DIAG-ENV-MISSING",
		"connect ECONNREFUSED 127.0.0.1:5432":                       "ZS-DIAG-DB",
		"Error: EACCES: permission denied, mkdir '/root/.config'":   "ZS-DIAG-EACCES",
		"npm warn EBADENGINE Unsupported engine { package: 'x' }":   "ZS-DIAG-ENGINE",
		"OSError: [Errno 98] Address already in use":                "ZS-DIAG-EADDRINUSE",
		"django.core.exceptions.DisallowedHost: Invalid HTTP_HOST header: '127.0.0.1:4317'. You may need to add '127.0.0.1' to ALLOWED_HOSTS.": "ZS-DIAG-DISALLOWED-HOST",
	}
	for log, want := range cases {
		stage := "start"
		if want == "ZS-DIAG-LOCKFILE" || want == "ZS-DIAG-ENGINE" {
			stage = "install"
		}
		got := ids(Analyze(Input{Stage: stage, Logs: "some output\n" + log + "\nmore", ExitCode: 1, Exited: true}))
		d, ok := got[want]
		if !ok {
			t.Errorf("%q: want %s, got %v", log, want, got)
			continue
		}
		if len(d.Evidence) == 0 {
			t.Errorf("%s: missing evidence", want)
		}
	}
	env := ids(Analyze(Input{Stage: "start", Logs: "Error: Missing required environment variable DATABASE_URL"}))["ZS-DIAG-ENV-MISSING"]
	if env.Summary != "The application appears to require environment variable DATABASE_URL" {
		t.Errorf("env var name not extracted: %q", env.Summary)
	}
}

func TestUnknownIsHonest(t *testing.T) {
	ds := Analyze(Input{Stage: "verify", Logs: "nothing recognisable"})
	if len(ds) != 1 || ds[0].ID != "ZS-DIAG-UNKNOWN" || ds[0].Confidence != "low" {
		t.Fatalf("unmatched failures must be reported as unknown: %+v", ds)
	}
	step := Analyze(Input{Stage: "install", StepID: "install-deps", ExitCode: 2, Logs: "boom"})
	if step[0].ID != "ZS-DIAG-STEP-FAILED" || step[0].Confidence != "low" {
		t.Fatalf("generic step failure: %+v", step)
	}
}
