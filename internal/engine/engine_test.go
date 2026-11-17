package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerosetup/zerosetup/internal/passport"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/store"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

// fakeBackend simulates a container engine. "App" containers start a real
// HTTP server on the published loopback port so readiness is exercised
// against real sockets; listening state is reported via a synthetic
// /proc/net/tcp, as the real probe would see it.
type fakeBackend struct {
	mu       sync.Mutex
	behavior string // ok | loopback | http500 | crash | stepfail
	conts    map[string]*fakeCont
	volumes  map[string]map[string]string
	networks map[string]map[string]string
	seq      int
	specs    []runtime.ContainerSpec
}

type fakeCont struct {
	spec    runtime.ContainerSpec
	running bool
	exit    int
	srv     *http.Server
	env     map[string]string
}

func newFake(behavior string) *fakeBackend {
	return &fakeBackend{behavior: behavior, conts: map[string]*fakeCont{}, volumes: map[string]map[string]string{}, networks: map[string]map[string]string{}}
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Close() error { return nil }
func (f *fakeBackend) Check(context.Context) (*runtime.Info, error) {
	return &runtime.Info{Backend: "fake", ServerVersion: "1", OS: "linux", Arch: "arm64", MemoryLimit: true, PidsLimit: true, CgroupVersion: "2"}, nil
}
func (f *fakeBackend) EnsureImage(_ context.Context, ref string, p func(string)) (*runtime.ImageInfo, error) {
	return &runtime.ImageInfo{Reference: ref, ID: "sha256:img", Digest: "node@sha256:feed", OS: "linux", Arch: "arm64"}, nil
}
func (f *fakeBackend) EnsureNetwork(_ context.Context, n string, l map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[n] = l
	return nil
}
func (f *fakeBackend) EnsureVolume(_ context.Context, n string, l map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.volumes[n]; !ok {
		f.volumes[n] = l
	}
	return nil
}
func (f *fakeBackend) Create(_ context.Context, s runtime.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("c%011d", f.seq)
	env := map[string]string{}
	for _, kv := range s.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	f.conts[id] = &fakeCont{spec: s, env: env}
	f.specs = append(f.specs, s)
	return id, nil
}
func (f *fakeBackend) CopyTo(_ context.Context, id, dest string, r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}
func (f *fakeBackend) Start(_ context.Context, id string) error {
	f.mu.Lock()
	c := f.conts[id]
	f.mu.Unlock()
	role := c.spec.Labels[runtime.LabelRole]
	if role == "service" {
		c.running = true
		return nil
	}
	if role != "app" {
		c.exit = 0
		if f.behavior == "stepfail" && role == "step" && c.spec.Labels[runtime.LabelStep] == "install-deps" {
			c.exit = 1
		}
		return nil
	}
	if f.behavior == "crash" {
		c.exit = 1
		return nil
	}
	for _, hp := range c.spec.Ports {
		status := 200
		if f.behavior == "http500" {
			status = 500
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
		if err != nil {
			return zerr.Wrap(err, zerr.CodePortUnavailable, "start", "port taken")
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })}
		go srv.Serve(ln)
		c.srv = srv
	}
	c.running = true
	return nil
}
func (f *fakeBackend) Wait(_ context.Context, id string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conts[id].exit, nil
}
func (f *fakeBackend) Logs(_ context.Context, id string, _ bool, _ string, o, e io.Writer) error {
	f.mu.Lock()
	c := f.conts[id]
	f.mu.Unlock()
	if c == nil {
		return notFoundErr{}
	}
	if c.spec.Labels[runtime.LabelRole] == "step" {
		fmt.Fprintf(o, "running %s\nSECRET-IN-LOG: %s\n", strings.Join(c.spec.Cmd, " "), c.env["API_TOKEN"])
		if f.behavior == "stepfail" {
			fmt.Fprintln(e, "npm error `npm ci` can only install packages when your package.json and package-lock.json are in sync.")
		}
	} else {
		fmt.Fprintf(o, "app log line\n")
		if f.behavior == "crash" {
			fmt.Fprintln(e, "Error: Missing required environment variable DATABASE_URL")
		}
	}
	return nil
}
func (f *fakeBackend) Inspect(_ context.Context, id string) (*runtime.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return nil, notFoundErr{}
	}
	hp := map[int]int{}
	for k, v := range c.spec.Ports {
		hp[k] = v
	}
	return &runtime.ContainerState{ID: id, Running: c.running, ExitCode: c.exit, HostPorts: hp, Labels: c.spec.Labels}, nil
}

type notFoundErr struct{}

func (notFoundErr) Error() string  { return "not found" }
func (notFoundErr) NotFound() bool { return true }

func (f *fakeBackend) Exec(_ context.Context, id string, argv []string) ([]byte, int, error) {
	f.mu.Lock()
	c := f.conts[id]
	f.mu.Unlock()
	if c.spec.Labels[runtime.LabelRole] == "service" {
		return []byte("ok"), 0, nil
	}
	addr := "00000000"
	if f.behavior == "loopback" && c.env["HOST"] != "0.0.0.0" {
		addr = "0100007F"
	}
	var port int
	for cp := range c.spec.Ports {
		port = cp
	}
	return []byte(fmt.Sprintf("  sl  local_address rem_address   st\n   0: %s:%04X 00000000:0000 0A 0 0 0 0 0\n", addr, port)), 0, nil
}
func (f *fakeBackend) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.conts[id]; c != nil {
		c.running = false
		if c.srv != nil {
			c.srv.Close()
			c.srv = nil
		}
	}
	return nil
}
func (f *fakeBackend) Remove(ctx context.Context, id string) error {
	f.Stop(ctx, id, 0)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.conts, id)
	return nil
}
func (f *fakeBackend) RemoveVolume(_ context.Context, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, n)
	return nil
}
func (f *fakeBackend) RemoveNetwork(_ context.Context, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.networks, n)
	return nil
}
func match(l, filter map[string]string) bool {
	for k, v := range filter {
		if l[k] != v {
			return false
		}
	}
	return true
}
func (f *fakeBackend) ListOwned(_ context.Context, filter map[string]string) (*runtime.Owned, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := &runtime.Owned{}
	for id, c := range f.conts {
		if match(c.spec.Labels, filter) {
			o.Containers = append(o.Containers, runtime.ContainerState{ID: id, Labels: c.spec.Labels})
		}
	}
	for n, l := range f.volumes {
		if match(l, filter) {
			o.Volumes = append(o.Volumes, n)
		}
	}
	for n, l := range f.networks {
		if match(l, filter) {
			o.Networks = append(o.Networks, n)
		}
	}
	return o, nil
}
func (f *fakeBackend) Stats(context.Context, string) (*runtime.Stats, error) {
	return &runtime.Stats{MemoryBytes: 1}, nil
}

func newTestService(t *testing.T, fb *fakeBackend) *Service {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(PathsAt(home), st, func(context.Context) (runtime.Backend, error) { return fb, nil })
	t.Cleanup(func() { s.Close() })
	return s
}

func fixtureDir(name string) string {
	d, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", name))
	return d
}

func createSession(t *testing.T, s *Service, fixture string, req InspectRequest) *sessions.Session {
	t.Helper()
	req.Source = fixtureDir(fixture)
	res, err := s.Inspect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.CreateSession(context.Background(), req, res)
	if err != nil {
		t.Fatal(err)
	}
	if x.State != sessions.AwaitingApproval {
		t.Fatalf("new session state %s", x.State)
	}
	return x
}

func TestExecuteRequiresApproval(t *testing.T) {
	s := newTestService(t, newFake("ok"))
	x := createSession(t, s, "express", InspectRequest{})
	if _, err := s.Execute(context.Background(), x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("unapproved execution must be refused: %v", err)
	}
	if _, err := s.Approve(context.Background(), x.ID, "000000000000"); !zerr.HasCode(err, zerr.CodeApprovalMismatch) {
		t.Fatalf("wrong hash must be refused: %v", err)
	}
}

func TestExecuteHappyPathStopRestartRemove(t *testing.T) {
	fb := newFake("ok")
	s := newTestService(t, fb)
	ctx := context.Background()
	x := createSession(t, s, "express", InspectRequest{Overrides: plannerSecret("API_TOKEN")})
	if _, err := s.Approve(ctx, x.ID, x.PlanHash[7:19]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("missing secret must be refused: %v", err)
	}
	x, err := s.Execute(ctx, x.ID, map[string]string{"API_TOKEN": "tok-very-secret"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if x.State != sessions.Ready || !strings.HasPrefix(x.URL, "http://127.0.0.1:") {
		t.Fatalf("state=%s url=%s", x.State, x.URL)
	}
	for _, m := range []sessions.Milestone{sessions.MilestoneImageReady, sessions.MilestoneDepsInstalled, sessions.MilestoneProcessStarted,
		sessions.MilestonePortListening, sessions.MilestoneHTTPReady, sessions.MilestoneHealthy} {
		if _, ok := x.Milestones[m]; !ok {
			t.Errorf("missing milestone %s", m)
		}
	}
	// The preview really answers on the URL.
	resp, err := http.Get(x.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("preview not reachable: %v", err)
	}
	resp.Body.Close()
	// Hardening is applied to every container; secrets never reach steps.
	for _, sp := range fb.specs {
		if sp.Labels[runtime.LabelSession] != x.ID || sp.Labels[runtime.LabelManaged] != "true" {
			t.Errorf("container %s not labelled", sp.Name)
		}
		for _, e := range sp.Env {
			if strings.Contains(e, "tok-very-secret") && sp.Labels[runtime.LabelRole] != "app" {
				t.Errorf("secret injected into non-app container %s", sp.Name)
			}
		}
	}
	// Logs and events are redacted.
	evs, _ := s.Store.Events(ctx, x.ID, 0, 10000)
	for _, e := range evs {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "tok-very-secret") {
			t.Fatalf("secret leaked into event: %s", b)
		}
	}
	// Passport is valid and secret-free.
	b, pp, err := s.Passport(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "tok-very-secret") || passport.Validate(pp) != nil {
		t.Fatal("passport invalid or leaks secret")
	}
	var round passport.Passport
	if err := json.Unmarshal(b, &round); err != nil || round.Execution.PlanHash != x.PlanHash || round.Environment.BaseImageDigest != "node@sha256:feed" {
		t.Fatalf("passport content: %v %+v", err, round.Execution)
	}

	if x, err = s.Stop(ctx, x.ID); err != nil || x.State != sessions.Stopped {
		t.Fatalf("stop: %v %s", err, x.State)
	}
	if _, err := s.Restart(ctx, x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("restart without secrets must be refused: %v", err)
	}
	if x, err = s.Restart(ctx, x.ID, map[string]string{"API_TOKEN": "tok-very-secret"}); err != nil || x.State != sessions.Ready {
		t.Fatalf("restart: %v", err)
	}
	res, err := s.Remove(ctx, x.ID)
	if err != nil || !res.Verified {
		t.Fatalf("remove: %v %+v", err, res)
	}
	left, _ := fb.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: x.ID})
	if !left.Empty() {
		t.Fatalf("resources left behind: %+v", left)
	}
	// The shared cache volume is not a session resource and survives.
	if _, ok := fb.volumes["zerosetup-cache-node"]; !ok {
		t.Fatal("cache volume must survive session removal")
	}
	if x, _ = s.Get(ctx, x.ID); x.State != sessions.Removed {
		t.Fatalf("final state %s", x.State)
	}
}

func TestLoopbackFailureAndRepair(t *testing.T) {
	fb := newFake("loopback")
	s := newTestService(t, fb)
	ctx := context.Background()
	x := createSession(t, s, "broken-port", InspectRequest{})
	s.Approve(ctx, x.ID, x.PlanHash)
	start := time.Now()
	x, err := s.Execute(ctx, x.ID, nil)
	if !zerr.HasCode(err, zerr.CodeNotReady) || x.State != sessions.Failed {
		t.Fatalf("expected NOT_READY failure, got %v state=%s", err, x.State)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("loopback bind should fail fast, not wait for the full timeout")
	}
	if _, ok := x.Milestones[sessions.MilestoneHTTPReady]; ok {
		t.Fatal("failed session must not record HTTP_READY")
	}
	diags, _ := s.Diagnostics(ctx, x.ID)
	if len(diags) == 0 || diags[0].ID != "ZS-DIAG-LOOPBACK" || diags[0].Repair == nil {
		t.Fatalf("diagnostics: %+v", diags)
	}
	out, err := s.ApplyRepair(ctx, x.ID, "bind-all-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	if out.NewHash == out.OldHash || out.Session.State != sessions.AwaitingApproval || out.Session.ApprovedAt != nil {
		t.Fatal("repair must produce a new plan requiring approval")
	}
	if _, err := s.Execute(ctx, x.ID, nil); !zerr.HasCode(err, zerr.CodeApprovalRequired) {
		t.Fatalf("repaired plan must need approval: %v", err)
	}
	s.Approve(ctx, x.ID, out.NewHash)
	x, err = s.Execute(ctx, x.ID, nil)
	if err != nil || x.State != sessions.Ready {
		t.Fatalf("after repair: %v %s", err, x.State)
	}
	f := FactsOf(x)
	// The repair changed the environment, which can affect installs, so
	// dependencies are conservatively reinstalled (correctness over speed).
	if f.Cache == nil || f.Cache.DepsReused || !strings.Contains(f.Cache.Reason, "changed") {
		t.Fatalf("changed inputs must trigger a reinstall with an explanation: %+v", f.Cache)
	}
	s.Stop(ctx, x.ID)
	if _, err := s.RevertRepair(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	if y, _ := s.Get(ctx, x.ID); y.PlanHash != out.OldHash {
		t.Fatal("revert must restore the previous plan")
	}
}

func TestCrashAndStepFailure(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFake("crash"))
	x := createSession(t, s, "express", InspectRequest{})
	s.Approve(ctx, x.ID, x.PlanHash)
	x, err := s.Execute(ctx, x.ID, nil)
	if err == nil || x.State != sessions.Failed {
		t.Fatalf("crash: %v %s", err, x.State)
	}
	found := map[string]bool{}
	for _, d := range FactsOf(x).Diagnostics {
		found[d.ID] = true
	}
	if !found["ZS-DIAG-CRASH"] || !found["ZS-DIAG-ENV-MISSING"] {
		t.Fatalf("crash diagnostics: %+v", found)
	}
	// Retrying the same approved plan reuses the successful install.
	x, _ = s.Execute(ctx, x.ID, nil)
	if c := FactsOf(x).Cache; c == nil || !c.DepsReused || x.Attempt != 2 {
		t.Fatalf("retry with identical inputs should reuse dependencies: %+v attempt=%d", c, x.Attempt)
	}

	s2 := newTestService(t, newFake("stepfail"))
	y := createSession(t, s2, "express", InspectRequest{})
	s2.Approve(ctx, y.ID, y.PlanHash)
	y, err = s2.Execute(ctx, y.ID, nil)
	if !zerr.HasCode(err, zerr.CodeStepFailed) || y.State != sessions.Failed || y.ContainerID != "" {
		t.Fatalf("step failure: %v %s", err, y.State)
	}
	if d := FactsOf(y).Diagnostics; len(d) == 0 || d[0].ID != "ZS-DIAG-LOCKFILE" {
		t.Fatalf("lockfile diagnostic: %+v", d)
	}
}

func TestHTTP500IsNotReady(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFake("http500"))
	x := createSession(t, s, "unhealthy", InspectRequest{Overrides: plannerTimeout(3)})
	s.Approve(ctx, x.ID, x.PlanHash)
	x, err := s.Execute(ctx, x.ID, nil)
	if !zerr.HasCode(err, zerr.CodeNotReady) || x.State == sessions.Ready {
		t.Fatalf("a 500-only server must not be READY: %v %s", err, x.State)
	}
	if _, ok := x.Milestones[sessions.MilestonePortListening]; !ok {
		t.Fatal("port listening milestone should be recorded")
	}
	if d := FactsOf(x).Diagnostics; len(d) == 0 || d[0].ID != "ZS-DIAG-HTTP-NOT-READY" {
		t.Fatalf("diagnostics: %+v", d)
	}
}

func TestReconcileDetectsExitedContainer(t *testing.T) {
	ctx := context.Background()
	fb := newFake("ok")
	s := newTestService(t, fb)
	x := createSession(t, s, "express", InspectRequest{})
	s.Approve(ctx, x.ID, x.PlanHash)
	x, _ = s.Execute(ctx, x.ID, nil)
	fb.Stop(ctx, x.ContainerID, 0) // simulate an unexpected exit
	y, err := s.Status(ctx, x.ID)
	if err != nil || y.State != sessions.Stopped {
		t.Fatalf("persisted READY must be reconciled: %v %s", err, y.State)
	}
	fb.Remove(ctx, x.ContainerID)
	z, _ := s.Get(ctx, x.ID)
	z.State = sessions.Ready
	s.Store.Save(ctx, z)
	if z, _ = s.Status(ctx, x.ID); z.State != sessions.Failed {
		t.Fatalf("missing container must mark the session FAILED, got %s", z.State)
	}
}

func plannerSecret(name string) planner.Overrides {
	return planner.Overrides{SecretNames: []string{name}}
}
func plannerTimeout(sec int) planner.Overrides { return planner.Overrides{ReadyTimeoutSec: sec} }
