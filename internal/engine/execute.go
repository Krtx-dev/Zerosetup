package engine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerosetup/zerosetup/internal/diagnostics"
	"github.com/zerosetup/zerosetup/internal/policy"
	"github.com/zerosetup/zerosetup/internal/readiness"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Resource names. Every resource is labelled with the session ID as well.
func netName(sid string) string         { return "zerosetup-" + sid }
func wsVolume(sid string) string        { return "zerosetup-" + sid + "-ws" }
func appName(sid string) string         { return "zerosetup-" + sid + "-app" }
func cacheVolume(runtime string) string { return "zerosetup-cache-" + runtime }

func labels(sid, role string) map[string]string {
	return map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: sid, runtime.LabelRole: role}
}

// Approve binds approval to the session's exact plan hash. hash may be the
// full "sha256:..." form or the 12-character short form shown to users.
func (s *Service) Approve(ctx context.Context, sid, hash string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		return nil, zerr.New(zerr.CodeApprovalRequired, "approval", "approval requires the plan hash")
	}
	if hash != x.PlanHash && hash != ps.ShortHash(x.PlanHash) {
		return nil, zerr.New(zerr.CodeApprovalMismatch, "approval", "approved hash does not match the session's plan").
			WithDetail("session plan is %s; approval was for %s", ps.ShortHash(x.PlanHash), hash).
			WithSuggestion("Review the current plan with `zerosetup status " + sid + "` and approve its hash.")
	}
	switch x.State {
	case sessions.AwaitingApproval, sessions.Failed, sessions.Stopped, sessions.Cancelled:
	default:
		return nil, zerr.New(zerr.CodeIllegalState, "approval", "session "+sid+" is "+string(x.State)+"; nothing to approve")
	}
	now := time.Now().UTC()
	x.ApprovedAt = &now
	if err := s.Store.Save(ctx, x); err != nil {
		return nil, err
	}
	s.emit(ctx, sid, "stage", "approval", "execution approved for plan "+ps.ShortHash(x.PlanHash), map[string]any{"planHash": x.PlanHash})
	return x, nil
}

// IsRunningHere reports whether this process is executing sid.
func (s *Service) IsRunningHere(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[sid]
	return ok
}

// Cancel cancels an execution running in this process.
func (s *Service) Cancel(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.running[sid]; ok {
		c()
		return true
	}
	return false
}

// Execute runs an approved session to READY (or FAILED). secrets maps
// secret env names to values; values are never persisted.
func (s *Service) Execute(ctx context.Context, sid string, secrets map[string]string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if x.ApprovedAt == nil {
		return nil, zerr.New(zerr.CodeApprovalRequired, "approval", "session "+sid+" has not been approved").
			WithSuggestion("Execution of repository code requires explicit approval of the plan.")
	}
	switch x.State {
	case sessions.AwaitingApproval, sessions.Failed, sessions.Stopped, sessions.Cancelled:
	default:
		return nil, zerr.New(zerr.CodeIllegalState, "execute", "session "+sid+" is "+string(x.State)+" and cannot be executed now")
	}
	plan, err := s.Plan(ctx, x)
	if err != nil {
		return nil, err
	}
	if err := policy.Enforce(plan); err != nil {
		return nil, err
	}
	var secretVals []string
	for _, e := range plan.Env {
		if e.Secret && e.Required {
			v, ok := secrets[e.Name]
			if !ok || v == "" {
				return nil, zerr.New(zerr.CodeApprovalRequired, "execute", "secret "+e.Name+" was not provided").
					WithSuggestion("Provide it with --secret " + e.Name + " (you will be prompted) or --secret-from-env " + e.Name + ".")
			}
		}
	}
	for _, v := range secrets {
		secretVals = append(secretVals, v)
	}

	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, busy := s.running[sid]; busy {
		s.mu.Unlock()
		cancel()
		return nil, zerr.New(zerr.CodeIllegalState, "execute", "session "+sid+" is already executing")
	}
	s.running[sid] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, sid)
		s.mu.Unlock()
		cancel()
	}()

	r := &run{s: s, x: x, plan: plan, facts: loadFacts(x), secrets: secrets, secretVals: secretVals, start: time.Now()}
	x.Attempt++
	x.RunnerPID = os.Getpid()
	x.Error = nil
	x.URL, x.HostPort, x.ContainerID = "", 0, ""
	x.Milestones = map[sessions.Milestone]time.Time{}
	r.facts.Diagnostics = nil
	r.facts.Readiness = nil
	r.facts.Steps = nil
	err = r.execute(ctx)
	x.RunnerPID = 0
	saveFacts(x, r.facts)
	if err != nil {
		if errors.Is(err, context.Canceled) || zerr.HasCode(err, zerr.CodeCancelled) {
			r.cleanupContainers(ctx, true)
			x.Error = zerr.New(zerr.CodeCancelled, r.stage, "execution cancelled by user")
			_ = s.transition(ctx, x, sessions.Cancelled, "execution cancelled; containers removed")
			return x, x.Error
		}
		ze, ok := zerr.As(err)
		if !ok {
			ze = zerr.Wrap(err, zerr.CodeInternal, r.stage, "execution failed")
		}
		if r.b != nil {
			stopServices(context.WithoutCancel(ctx), r.b, r.facts)
		}
		x.Error = ze
		saveFacts(x, r.facts)
		if x.State != sessions.Failed {
			_ = s.transition(ctx, x, sessions.Failed, ze.Summary)
		}
		return x, ze
	}
	_ = s.Store.RecordMetric(context.WithoutCancel(ctx), sid, x.Attempt, "total_to_ready", time.Since(r.start))
	if err := s.transition(ctx, x, sessions.Ready, "application ready at "+x.URL); err != nil {
		return x, err
	}
	return x, nil
}

type run struct {
	s          *Service
	x          *sessions.Session
	plan       *ps.Plan
	facts      *Facts
	secrets    map[string]string
	secretVals []string
	b          runtime.Backend
	stage      string
	start      time.Time
}

func (r *run) milestone(ctx context.Context, m sessions.Milestone, msg string) {
	now := time.Now().UTC()
	r.x.Milestones[m] = now
	_ = r.s.Store.Save(context.WithoutCancel(ctx), r.x)
	r.s.emit(ctx, r.x.ID, "milestone", r.stage, msg, map[string]any{"milestone": string(m)})
}

func (r *run) metric(ctx context.Context, name string, d time.Duration) {
	_ = r.s.Store.RecordMetric(context.WithoutCancel(ctx), r.x.ID, r.x.Attempt, name, d)
}

// containerEnv is the fixed runtime environment plus plan env and secrets.
func containerEnv(p *ps.Plan, secrets map[string]string, step bool) []string {
	env := []string{
		"HOME=" + ps.HomeDir,
		"PATH=" + ps.ToolsDir + "/bin:" + ps.VenvDir + "/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"npm_config_cache=" + ps.CacheDir + "/npm",
		"npm_config_prefix=" + ps.ToolsDir,
		"npm_config_update_notifier=false",
		"YARN_CACHE_FOLDER=" + ps.CacheDir + "/yarn",
		"PIP_CACHE_DIR=" + ps.CacheDir + "/pip",
		"UV_CACHE_DIR=" + ps.CacheDir + "/uv",
		"XDG_CACHE_HOME=" + ps.CacheDir + "/xdg",
		"PYTHONUNBUFFERED=1",
	}
	if p.Stack.PackageManager == "pnpm" {
		// pnpm reads npm_config_*; npm itself warns about unknown keys.
		env = append(env, "npm_config_store_dir="+ps.CacheDir+"/pnpm-store")
	}
	if step {
		env = append(env, "CI=true")
	}
	for _, e := range p.Env {
		if e.Secret {
			continue
		}
		env = append(env, e.Name+"="+e.Value)
	}
	if !step {
		names := make([]string, 0, len(secrets))
		for k := range secrets {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			env = append(env, k+"="+secrets[k])
		}
	}
	return env
}

// FixedEnv exposes the fixed (non-plan) environment for display.
func FixedEnv() []string { return containerEnv(&ps.Plan{}, nil, false) }

func (r *run) baseSpec(name, role string) runtime.ContainerSpec {
	p := r.plan
	return runtime.ContainerSpec{
		Name: name, Image: p.Image.Reference, User: fmt.Sprintf("%d:%d", ps.ContainerUID, ps.ContainerUID),
		Mounts:  []runtime.VolumeMount{{Volume: wsVolume(r.x.ID), Target: ps.WorkspaceDir}, {Volume: cacheVolume(p.Stack.Runtime), Target: ps.CacheDir}},
		Network: netName(r.x.ID), CPUs: p.Resources.CPUs, MemoryMB: p.Resources.MemoryMB, PidsLimit: p.Resources.PidsLimit,
		LogMaxMB: p.Resources.LogMaxSizeMB, Labels: labels(r.x.ID, role),
	}
}

func workdir(c ps.Command) string {
	if c.Workdir == "" {
		return ps.SourceDir
	}
	return ps.SourceDir + "/" + c.Workdir
}

func (r *run) execute(ctx context.Context) error {
	s, x, p := r.s, r.x, r.plan
	// ── PREPARING ───────────────────────────────────────────────
	r.stage = "prepare"
	if err := s.transition(ctx, x, sessions.Preparing, "preparing environment"); err != nil {
		return err
	}
	b, err := s.Backend(ctx)
	if err != nil {
		return err
	}
	r.b = b
	info, err := b.Check(ctx)
	if err != nil {
		return err
	}
	r.facts.Engine = info
	if !info.MemoryLimit || !info.PidsLimit {
		// Fail closed: limits are a required security property.
		return zerr.New(zerr.CodePolicyViolation, "prepare", "the container engine cannot enforce memory/PIDs limits").
			WithSuggestion("Use an engine with cgroup support (Docker Desktop, OrbStack, Colima, or Linux Docker with cgroups v2).")
	}
	r.s.emit(ctx, x.ID, "stage", r.stage, fmt.Sprintf("container engine %s %s (%s, %s)", info.Backend, info.ServerVersion, info.OS, info.Arch), nil)

	t := time.Now()
	r.stage = "image"
	r.s.emit(ctx, x.ID, "stage", r.stage, "acquiring image "+p.Image.Reference, nil)
	img, err := b.EnsureImage(ctx, p.Image.Reference, func(m string) {
		r.s.emit(ctx, x.ID, "progress", "image", m, nil)
	})
	if err != nil {
		return err
	}
	r.facts.Image = img
	x.ImageDigest = img.Digest
	how := "already present"
	if img.Pulled {
		how = "pulled"
	}
	r.metric(ctx, "image", time.Since(t))
	r.milestone(ctx, sessions.MilestoneImageReady, fmt.Sprintf("image %s (%s, %s/%s)", how, img.Digest, img.OS, img.Arch))

	r.stage = "prepare"
	t = time.Now()
	r.cleanupContainers(ctx, false)
	if err := b.EnsureNetwork(ctx, netName(x.ID), labels(x.ID, "network")); err != nil {
		return err
	}
	if err := b.EnsureVolume(ctx, wsVolume(x.ID), labels(x.ID, "workspace")); err != nil {
		return err
	}
	cv := cacheVolume(p.Stack.Runtime)
	owned, err := b.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelRole: "cache"})
	if err != nil {
		return err
	}
	existed := false
	for _, v := range owned.Volumes {
		existed = existed || v == cv
	}
	if err := b.EnsureVolume(ctx, cv, map[string]string{runtime.LabelManaged: "true", runtime.LabelRole: "cache", "io.zerosetup.cache": p.Stack.Runtime}); err != nil {
		return err
	}
	depsKey := r.depsKey()
	prev := r.facts.Cache
	// Reuse installed dependencies only if this session's workspace already
	// holds a successful install produced from identical inputs.
	reuse := prev != nil && prev.InstallOK && prev.DepsKey == depsKey
	r.facts.Cache = &CacheRecord{Volume: cv, VolumeExisted: existed, DepsKey: depsKey}
	if err := r.copySource(ctx); err != nil {
		return err
	}
	r.metric(ctx, "prepare", time.Since(t))
	if existed {
		r.s.emit(ctx, x.ID, "stage", "cache", "package cache volume "+cv+" is warm (reused from earlier runs)", nil)
	} else {
		r.s.emit(ctx, x.ID, "stage", "cache", "package cache volume "+cv+" created (cold cache)", nil)
	}

	// ── BUILDING ────────────────────────────────────────────────
	r.stage = "install"
	if err := s.transition(ctx, x, sessions.Building, "installing dependencies"); err != nil {
		return err
	}
	if reuse {
		r.facts.Cache.DepsReused = true
		r.facts.Cache.Reason = "dependency inputs unchanged since the last successful install in this session (same image, manifests and install commands); install steps skipped"
		r.s.emit(ctx, x.ID, "stage", "cache", "cache hit: "+r.facts.Cache.Reason, nil)
	} else if prev != nil && prev.DepsKey != "" && prev.DepsKey != depsKey {
		r.facts.Cache.Reason = "dependency inputs changed since the last install; reinstalling"
	} else {
		r.facts.Cache.Reason = "no previous install in this session"
	}
	t = time.Now()
	for _, st := range p.Steps {
		r.stage = st.Stage
		if reuse && st.Stage == ps.StageInstall {
			r.facts.Steps = append(r.facts.Steps, StepRecord{ID: st.ID, Name: st.Name, Stage: st.Stage, Skipped: true, SkipReason: "dependency cache hit"})
			continue
		}
		if err := r.runStep(ctx, st); err != nil {
			return err
		}
	}
	r.metric(ctx, "install", time.Since(t))
	r.facts.Cache.InstallOK = true
	r.milestone(ctx, sessions.MilestoneDepsInstalled, "dependencies installed")
	hasBuild := false
	for _, st := range p.Steps {
		hasBuild = hasBuild || st.Stage == ps.StageBuild
	}
	if hasBuild {
		r.milestone(ctx, sessions.MilestoneBuildSuccess, "build steps succeeded")
	}
	saveFacts(x, r.facts)

	// ── STARTING ────────────────────────────────────────────────
	r.stage = "start"
	if err := s.transition(ctx, x, sessions.Starting, "starting application"); err != nil {
		return err
	}
	t = time.Now()
	r.stage = "services"
	if err := s.startServices(ctx, r.b, x.ID, p, r.facts, true); err != nil {
		return err
	}
	r.stage = "start"
	if len(p.Services) > 0 {
		r.metric(ctx, "services", time.Since(t))
		t = time.Now()
	}
	if err := r.startApp(ctx); err != nil {
		return err
	}
	r.metric(ctx, "start", time.Since(t))

	// ── VERIFYING ───────────────────────────────────────────────
	r.stage = "verify"
	if err := s.transition(ctx, x, sessions.Verifying, "verifying readiness"); err != nil {
		return err
	}
	t = time.Now()
	if err := s.verify(ctx, r.b, x, p, r.facts, r.secretVals); err != nil {
		return err
	}
	r.metric(ctx, "readiness", time.Since(t))
	return nil
}

// depsKey identifies the inputs that determine installed dependencies.
func (r *run) depsKey() string {
	h := sha256.New()
	fmt.Fprintln(h, r.plan.Image.Reference, r.facts.Image.Digest)
	for _, m := range r.facts.Manifests {
		fmt.Fprintln(h, m.File, m.SHA256)
	}
	for _, st := range r.plan.Steps {
		if st.Stage == ps.StageInstall {
			fmt.Fprintln(h, strings.Join(st.Command.Argv, "\x00"))
		}
	}
	for _, e := range r.plan.Env {
		if !e.Secret {
			fmt.Fprintln(h, e.Name, e.Value)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// copySource copies the repository into the workspace volume through a
// one-shot prep container that only runs chown on ZEROSETUP's volume roots.
func (r *run) copySource(ctx context.Context) error {
	spec := r.baseSpec(fmt.Sprintf("zerosetup-%s-prep-%d", r.x.ID, r.x.Attempt), "prep")
	spec.User = "0:0"
	spec.Network = ""
	spec.PrepChown = true
	spec.Cmd = []string{"chown", fmt.Sprintf("%d:%d", ps.ContainerUID, ps.ContainerUID), ps.WorkspaceDir, ps.CacheDir}
	spec.CPUs, spec.MemoryMB, spec.PidsLimit = 1, 256, 64
	id, err := r.b.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer r.b.Remove(context.WithoutCancel(ctx), id)
	pr, pw := io.Pipe()
	var pack *PackResult
	var packErr error
	go func() {
		pack, packErr = writeWorkspaceTar(pw, r.facts.SourceDir)
		pw.CloseWithError(packErr)
	}()
	if err := r.b.CopyTo(ctx, id, ps.WorkspaceDir, pr); err != nil {
		pr.CloseWithError(err)
		if packErr != nil {
			return zerr.Wrap(packErr, zerr.CodeContainer, "prepare", "cannot package source")
		}
		return err
	}
	if packErr != nil {
		return zerr.Wrap(packErr, zerr.CodeContainer, "prepare", "cannot package source")
	}
	r.facts.Pack = pack
	msg := fmt.Sprintf("copied %d files (%s) into workspace volume", pack.Files, humanBytes(pack.Bytes))
	if len(pack.Skipped) > 0 {
		msg += fmt.Sprintf("; skipped %d (see passport)", len(pack.Skipped))
	}
	r.s.emit(ctx, r.x.ID, "stage", "prepare", msg, map[string]any{"skipped": pack.Skipped})
	if err := r.b.Start(ctx, id); err != nil {
		return err
	}
	code, err := r.b.Wait(ctx, id)
	if err != nil {
		return zerr.Wrap(err, zerr.CodeContainer, "prepare", "workspace preparation failed")
	}
	if code != 0 {
		return zerr.New(zerr.CodeContainer, "prepare", fmt.Sprintf("workspace preparation exited with code %d", code))
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// lineEmitter batches log lines into events so the dashboard can stream
// them without one database write per line.
type lineEmitter struct {
	s      *Service
	ctx    context.Context
	sid    string
	stage  string
	stream string
	mu     sync.Mutex
	buf    []string
	part   string
	last   time.Time
}

func (l *lineEmitter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data := l.part + string(p)
	lines := strings.Split(data, "\n")
	l.part = lines[len(lines)-1]
	l.buf = append(l.buf, lines[:len(lines)-1]...)
	if len(l.buf) >= 200 || time.Since(l.last) > 300*time.Millisecond {
		l.flushLocked()
	}
	return len(p), nil
}

func (l *lineEmitter) flushLocked() {
	if len(l.buf) == 0 {
		return
	}
	lines := l.buf
	l.buf = nil
	l.last = time.Now()
	l.s.emit(l.ctx, l.sid, "log", l.stage, lines[len(lines)-1], map[string]any{"stream": l.stream, "lines": lines})
}

func (l *lineEmitter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.part != "" {
		l.buf = append(l.buf, l.part)
		l.part = ""
	}
	l.flushLocked()
}

// runStep executes one plan step in its own hardened container.
func (r *run) runStep(ctx context.Context, st ps.Step) error {
	if err := os.MkdirAll(r.s.logDir(r.x.ID), 0o700); err != nil {
		return zerr.Wrap(err, zerr.CodeStore, "prepare", "cannot create log directory")
	}
	logPath := filepath.Join(r.s.logDir(r.x.ID), fmt.Sprintf("%d-%s.log", r.x.Attempt, st.ID))
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return zerr.Wrap(err, zerr.CodeStore, "prepare", "cannot create log file")
	}
	defer lf.Close()
	r.s.emit(ctx, r.x.ID, "stage", st.Stage, st.Name+": "+strings.Join(st.Command.Argv, " "), map[string]any{"step": st.ID, "argv": st.Command.Argv})
	spec := r.baseSpec(fmt.Sprintf("zerosetup-%s-%s-%d", r.x.ID, st.ID, r.x.Attempt), "step")
	spec.Labels[runtime.LabelStep] = st.ID
	spec.Cmd = st.Command.Argv
	spec.Workdir = workdir(st.Command)
	spec.Env = containerEnv(r.plan, nil, true)
	start := time.Now()
	id, err := r.b.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer r.b.Remove(context.WithoutCancel(ctx), id)
	if err := r.b.Start(ctx, id); err != nil {
		return err
	}
	em := &lineEmitter{s: r.s, ctx: ctx, sid: r.x.ID, stage: st.Stage, stream: st.ID}
	red := NewRedactor(io.MultiWriter(lf, em), r.secretVals)
	logDone := make(chan struct{})
	go func() {
		_ = r.b.Logs(ctx, id, true, "all", red, red)
		red.Flush()
		em.Flush()
		close(logDone)
	}()
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(st.TimeoutSec)*time.Second)
	defer cancel()
	code, werr := r.b.Wait(stepCtx, id)
	if werr != nil {
		_ = r.b.Stop(context.WithoutCancel(ctx), id, 2*time.Second)
	}
	select {
	case <-logDone:
	case <-time.After(5 * time.Second):
	}
	dur := time.Since(start)
	rec := StepRecord{ID: st.ID, Name: st.Name, Stage: st.Stage, ExitCode: code, DurationMs: dur.Milliseconds(), LogFile: logPath}
	r.facts.Steps = append(r.facts.Steps, rec)
	r.metric(ctx, "step:"+st.ID, dur)
	if werr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(werr, context.DeadlineExceeded) {
			r.facts.Diagnostics = []diagnostics.Diagnostic{{ID: "ZS-DIAG-TIMEOUT", Category: "build-timeout", Severity: diagnostics.SevError, Stage: st.Stage, Confidence: "high",
				Summary: fmt.Sprintf("Step %q exceeded its %ds timeout", st.Name, st.TimeoutSec), LikelyCause: "Slow network or a very large dependency tree.",
				ProposedResolution: "Retry; if it persists, check network access from the container engine."}}
			r.emitDiagnostics(ctx)
			return zerr.New(zerr.CodeStepFailed, st.Stage, fmt.Sprintf("%s timed out after %ds", st.Name, st.TimeoutSec))
		}
		return zerr.Wrap(werr, zerr.CodeStepFailed, st.Stage, st.Name+" failed")
	}
	r.s.emit(ctx, r.x.ID, "stage", st.Stage, fmt.Sprintf("%s finished with exit code %d in %s", st.Name, code, dur.Round(100*time.Millisecond)),
		map[string]any{"step": st.ID, "exitCode": code, "durationMs": dur.Milliseconds()})
	if code != 0 {
		r.facts.Diagnostics = diagnostics.Analyze(diagnostics.Input{Stage: st.Stage, StepID: st.ID, ExitCode: code,
			Logs: tailFile(logPath, 300), PackageManager: r.plan.Stack.PackageManager})
		r.emitDiagnostics(ctx)
		return zerr.New(zerr.CodeStepFailed, st.Stage, fmt.Sprintf("%s exited with code %d", st.Name, code)).
			WithSuggestion("See diagnostics with `zerosetup status " + r.x.ID + "` and the log with `zerosetup logs " + r.x.ID + " --step " + st.ID + "`.")
	}
	return nil
}

func (r *run) emitDiagnostics(ctx context.Context) {
	for _, d := range r.facts.Diagnostics {
		r.s.emit(ctx, r.x.ID, "diagnostic", d.Stage, d.Summary, map[string]any{"id": d.ID, "severity": d.Severity})
	}
}

func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// pickHostPort finds a free loopback port, avoiding ports owned by other
// live sessions.
func (s *Service) pickHostPort(ctx context.Context, avoid map[int]bool) (int, error) {
	used := map[int]bool{}
	for k := range avoid {
		used[k] = true
	}
	if list, err := s.Store.List(ctx, false, 500); err == nil {
		for _, o := range list {
			if o.HostPort > 0 && o.State.Active() {
				used[o.HostPort] = true
			}
		}
	}
	for p := 4317; p < 4317+500; p++ {
		if used[p] {
			continue
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		l.Close()
		return p, nil
	}
	return 0, zerr.New(zerr.CodePortUnavailable, "start", "no free loopback port in 4317-4816")
}

func (r *run) startApp(ctx context.Context) error {
	tried := map[int]bool{}
	for attempt := 0; attempt < 3; attempt++ {
		hp, err := r.s.pickHostPort(ctx, tried)
		if err != nil {
			return err
		}
		tried[hp] = true
		spec := r.baseSpec(appName(r.x.ID), "app")
		spec.Cmd = r.plan.Start.Argv
		spec.Workdir = workdir(r.plan.Start)
		spec.Env = containerEnv(r.plan, r.secrets, false)
		spec.Ports = map[int]int{r.plan.PrimaryPort(): hp}
		id, err := r.b.Create(ctx, spec)
		if err != nil {
			return err
		}
		if err := r.b.Start(ctx, id); err != nil {
			_ = r.b.Remove(context.WithoutCancel(ctx), id)
			if zerr.HasCode(err, zerr.CodePortUnavailable) {
				r.s.emit(ctx, r.x.ID, "warning", "start", fmt.Sprintf("host port %d was taken; retrying with another port", hp), nil)
				continue
			}
			return err
		}
		r.x.ContainerID = id
		r.x.HostPort = hp
		r.x.URL = "http://127.0.0.1:" + strconv.Itoa(hp)
		r.milestone(ctx, sessions.MilestoneProcessStarted, fmt.Sprintf("process started: %s (container %s, port %d → 127.0.0.1:%d)",
			strings.Join(r.plan.Start.Argv, " "), id[:12], r.plan.PrimaryPort(), hp))
		return nil
	}
	r.facts.Diagnostics = diagnostics.Analyze(diagnostics.Input{Stage: "start", PortConflict: true})
	return zerr.New(zerr.CodePortUnavailable, "start", "could not bind a host port after 3 attempts")
}

// cleanupContainers removes this session's containers (not volumes).
func (r *run) cleanupContainers(ctx context.Context, includeApp bool) {
	b := r.b
	if b == nil {
		var err error
		if b, err = r.s.Backend(ctx); err != nil {
			return
		}
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	owned, err := b.ListOwned(cctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: r.x.ID})
	if err != nil {
		return
	}
	for _, c := range owned.Containers {
		_ = b.Remove(cctx, c.ID)
	}
}

// verify runs readiness checks against a started app container.
func (s *Service) verify(ctx context.Context, b runtime.Backend, x *sessions.Session, p *ps.Plan, f *Facts, secretVals []string) error {
	rc := p.Readiness[0]
	port := rc.Port
	timeout := time.Duration(rc.TimeoutSec) * time.Second
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d%s", x.HostPort, rc.Path)
	rec := &ReadinessRecord{Path: rc.Path}
	f.Readiness = rec
	start := time.Now()
	bo := readiness.NewBackoff(2 * time.Second)
	var loopbackSince time.Time
	listening := false
	lastNote := time.Time{}
	fail := func(in diagnostics.Input, summary string) error {
		rec.DurationMs = time.Since(start).Milliseconds()
		rec.FailedReason = summary
		in.ExpectedPort, in.Listeners, in.TimeoutSec, in.MemoryMB = port, rec.Listeners, rc.TimeoutSec, p.Resources.MemoryMB
		in.LastHTTPStatus, in.LastHTTPError, in.ReadinessPath = rec.LastStatus, rec.LastError, rc.Path
		var sb strings.Builder
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = b.Logs(lctx, x.ContainerID, false, "300", &sb, &sb)
		cancel()
		in.Logs = RedactString(sb.String(), secretVals)
		f.Diagnostics = diagnostics.Analyze(in)
		for _, d := range f.Diagnostics {
			s.emit(ctx, x.ID, "diagnostic", "verify", d.Summary, map[string]any{"id": d.ID, "severity": d.Severity})
		}
		_ = b.Stop(context.WithoutCancel(ctx), x.ContainerID, 5*time.Second)
		return zerr.New(zerr.CodeNotReady, "verify", summary).
			WithSuggestion("Run `zerosetup status " + x.ID + "` for diagnostics and `zerosetup logs " + x.ID + "` for application output.")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec.Attempts++
		st, err := b.Inspect(ctx, x.ContainerID)
		if err != nil {
			return zerr.Wrap(err, zerr.CodeContainer, "verify", "cannot inspect application container")
		}
		if !st.Running {
			return fail(diagnostics.Input{Stage: "verify", Exited: true, ExitCode: st.ExitCode, OOMKilled: st.OOMKilled},
				fmt.Sprintf("application exited with code %d before becoming ready", st.ExitCode))
		}
		out, _, err := b.Exec(ctx, x.ContainerID, readiness.ProbeCommand)
		if err == nil {
			rec.Listeners = readiness.WithoutDockerDNS(readiness.ParseProcNetTCP(out))
		}
		switch readiness.Evaluate(rec.Listeners, port) {
		case readiness.PortReachable:
			loopbackSince = time.Time{}
			if !listening {
				listening = true
				x.Milestones[sessions.MilestonePortListening] = time.Now().UTC()
				_ = s.Store.Save(context.WithoutCancel(ctx), x)
				s.emit(ctx, x.ID, "milestone", "verify", fmt.Sprintf("port %d listening on all interfaces inside the container", port),
					map[string]any{"milestone": string(sessions.MilestonePortListening)})
			}
			hr := readiness.ProbeHTTP(ctx, url, 30*time.Second)
			rec.LastStatus, rec.LastError = hr.Status, hr.Err
			if hr.Status > 0 && hr.Status < 500 {
				rec.HTTPReady = true
				rec.Healthy = readiness.Healthy(hr, rc.ExpectStatus, rc.BodyContains)
				rec.DurationMs = time.Since(start).Milliseconds()
				x.Milestones[sessions.MilestoneHTTPReady] = time.Now().UTC()
				s.emit(ctx, x.ID, "milestone", "verify", fmt.Sprintf("HTTP %d from GET %s in %s", hr.Status, rc.Path, hr.Duration.Round(time.Millisecond)),
					map[string]any{"milestone": string(sessions.MilestoneHTTPReady), "status": hr.Status})
				if rec.Healthy {
					x.Milestones[sessions.MilestoneHealthy] = time.Now().UTC()
					s.emit(ctx, x.ID, "milestone", "verify", "application healthy", map[string]any{"milestone": string(sessions.MilestoneHealthy)})
				} else {
					s.emit(ctx, x.ID, "warning", "verify", fmt.Sprintf("server responded, but HTTP %d at %s is not a healthy status; reported as HTTP-ready, not healthy", hr.Status, rc.Path), nil)
				}
				return s.Store.Save(context.WithoutCancel(ctx), x)
			}
		case readiness.PortLoopbackOnly:
			if loopbackSince.IsZero() {
				loopbackSince = time.Now()
			} else if time.Since(loopbackSince) > 8*time.Second {
				return fail(diagnostics.Input{Stage: "verify"}, fmt.Sprintf("application listens on port %d only on 127.0.0.1 inside the container", port))
			}
		}
		if time.Now().After(deadline) {
			return fail(diagnostics.Input{Stage: "verify", TimedOut: true},
				fmt.Sprintf("application started but did not become HTTP-ready within %ds", rc.TimeoutSec))
		}
		if time.Since(lastNote) > 10*time.Second {
			lastNote = time.Now()
			desc := "no listening sockets yet"
			if len(rec.Listeners) > 0 {
				parts := make([]string, len(rec.Listeners))
				for i, l := range rec.Listeners {
					parts[i] = l.String()
				}
				desc = "listening: " + strings.Join(parts, ", ")
			}
			if rec.LastError != "" || rec.LastStatus >= 500 {
				desc += fmt.Sprintf("; last HTTP: %d %s", rec.LastStatus, rec.LastError)
			}
			s.emit(ctx, x.ID, "progress", "verify", fmt.Sprintf("waiting for readiness (%s elapsed, %s)", time.Since(start).Round(time.Second), desc), nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bo.Next()):
		}
	}
}
