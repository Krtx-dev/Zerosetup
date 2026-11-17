package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/runtime/docker"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

// Stop stops a session's application container, keeping its workspace.
func (s *Service) Stop(ctx context.Context, sid string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if s.Cancel(sid) {
		// Execution in this process: cancellation performs cleanup.
		for i := 0; i < 50 && s.IsRunningHere(sid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		return s.Get(ctx, sid)
	}
	if x.State.Executing() && pidAlive(x.RunnerPID) {
		return nil, zerr.New(zerr.CodeIllegalState, "stop", fmt.Sprintf("session %s is being executed by another ZEROSETUP process (pid %d)", sid, x.RunnerPID)).
			WithSuggestion("Interrupt that process (Ctrl-C), or wait for it to finish.")
	}
	if x.State == sessions.Stopped {
		return x, nil
	}
	b, err := s.Backend(ctx)
	if err != nil {
		return nil, err
	}
	if !sessions.CanTransition(x.State, sessions.Stopping) {
		x.State = sessions.Unknown // e.g. stale executing state; reconcile below
	}
	if err := s.transition(ctx, x, sessions.Stopping, "stopping application"); err != nil {
		return nil, err
	}
	if x.ContainerID != "" {
		if err := b.Stop(ctx, x.ContainerID, 10*time.Second); err != nil {
			_ = s.transition(ctx, x, sessions.Failed, "stop failed")
			return nil, err
		}
	}
	stopServices(ctx, b, loadFacts(x))
	st, err := b.Inspect(ctx, x.ContainerID)
	if err == nil && st.Running {
		return nil, zerr.New(zerr.CodeContainer, "stop", "container is still running after stop")
	}
	return x, s.transition(ctx, x, sessions.Stopped, "application stopped (workspace kept; `zerosetup restart` resumes it)")
}

// Restart (re)starts the existing application container and re-verifies.
func (s *Service) Restart(ctx context.Context, sid string, secrets map[string]string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if x.State == sessions.Ready {
		if _, err := s.Stop(ctx, sid); err != nil {
			return nil, err
		}
		if x, err = s.Get(ctx, sid); err != nil {
			return nil, err
		}
	}
	if x.State != sessions.Stopped {
		return nil, zerr.New(zerr.CodeIllegalState, "restart", "session is "+string(x.State)+"; only READY or STOPPED sessions can be restarted").
			WithSuggestion("Use `zerosetup run` to execute a failed session again.")
	}
	plan, err := s.Plan(ctx, x)
	if err != nil {
		return nil, err
	}
	// Secrets are not persisted, so a restart cannot reuse them.
	for _, e := range plan.Env {
		if e.Secret && e.Required && secrets[e.Name] == "" {
			return nil, zerr.New(zerr.CodeApprovalRequired, "restart", "the application uses secrets, which are never stored; run the session again with --secret").
				WithSuggestion("zerosetup run --session " + sid + " --secret " + e.Name)
		}
	}
	b, err := s.Backend(ctx)
	if err != nil {
		return nil, err
	}
	if x.ContainerID == "" {
		return nil, zerr.New(zerr.CodeContainer, "restart", "no application container recorded for this session")
	}
	if err := s.transition(ctx, x, sessions.Starting, "restarting application"); err != nil {
		return nil, err
	}
	if rf := loadFacts(x); len(plan.Services) > 0 {
		if err := s.startServices(ctx, b, x.ID, plan, rf, false); err != nil {
			x.Error, _ = zerr.As(err)
			saveFacts(x, rf)
			_ = s.transition(ctx, x, sessions.Failed, "service restart failed")
			return x, err
		}
		saveFacts(x, rf)
	}
	if err := b.Start(ctx, x.ContainerID); err != nil {
		x.Error, _ = zerr.As(err)
		_ = s.transition(ctx, x, sessions.Failed, "restart failed")
		return x, err
	}
	x.Milestones = map[sessions.Milestone]time.Time{sessions.MilestoneProcessStarted: time.Now().UTC()}
	st, err := b.Inspect(ctx, x.ContainerID)
	if err == nil {
		if hp, ok := st.HostPorts[plan.PrimaryPort()]; ok {
			x.HostPort = hp
			x.URL = fmt.Sprintf("http://127.0.0.1:%d", hp)
		}
	}
	if err := s.transition(ctx, x, sessions.Verifying, "verifying readiness"); err != nil {
		return nil, err
	}
	f := loadFacts(x)
	if err := s.verify(ctx, b, x, plan, f, nil); err != nil {
		x.Error, _ = zerr.As(err)
		saveFacts(x, f)
		_ = s.transition(ctx, x, sessions.Failed, "readiness failed after restart")
		return x, err
	}
	saveFacts(x, f)
	return x, s.transition(ctx, x, sessions.Ready, "application ready at "+x.URL)
}

// RemoveResult reports what cleanup removed and verified.
type RemoveResult struct {
	Removed  runtime.Owned `json:"removed"`
	Verified bool          `json:"verified"`
	Leftover runtime.Owned `json:"leftover"`
}

// Remove deletes every resource labelled with the session and verifies
// that none remain. Shared package caches are not session resources.
func (s *Service) Remove(ctx context.Context, sid string) (*RemoveResult, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if x.State == sessions.Removed {
		return &RemoveResult{Verified: true}, nil
	}
	s.Cancel(sid)
	if x.State.Executing() && pidAlive(x.RunnerPID) && !s.IsRunningHere(sid) {
		return nil, zerr.New(zerr.CodeIllegalState, "remove", fmt.Sprintf("session %s is being executed by another process (pid %d)", sid, x.RunnerPID))
	}
	b, err := s.Backend(ctx)
	if err != nil {
		return nil, err
	}
	if !sessions.CanTransition(x.State, sessions.Removing) {
		x.State = sessions.Unknown
	}
	if err := s.transition(ctx, x, sessions.Removing, "removing session resources"); err != nil {
		return nil, err
	}
	filter := map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: sid}
	owned, err := b.ListOwned(ctx, filter)
	if err != nil {
		return nil, err
	}
	res := &RemoveResult{Removed: *owned}
	var errs []string
	for _, c := range owned.Containers {
		if err := b.Remove(ctx, c.ID); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, v := range owned.Volumes {
		if err := b.RemoveVolume(ctx, v); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, n := range owned.Networks {
		if err := b.RemoveNetwork(ctx, n); err != nil {
			errs = append(errs, err.Error())
		}
	}
	left, err := b.ListOwned(ctx, filter)
	if err != nil {
		return nil, err
	}
	res.Leftover = *left
	res.Verified = left.Empty()
	if !res.Verified {
		x.Error = zerr.New(zerr.CodeCleanup, "cleanup", "some resources could not be removed").WithDetail("%s", strings.Join(errs, "; "))
		_ = s.transition(ctx, x, sessions.Failed, "cleanup incomplete")
		return res, x.Error
	}
	_ = os.RemoveAll(filepath.Join(s.Paths.Logs, sid))
	x.ContainerID, x.URL, x.HostPort = "", "", 0
	s.emit(ctx, sid, "stage", "cleanup", fmt.Sprintf("removed %d containers, %d volumes, %d networks; verified none remain",
		len(owned.Containers), len(owned.Volumes), len(owned.Networks)), nil)
	return res, s.transition(ctx, x, sessions.Removed, "session removed")
}

// Reconcile compares persisted state with the container engine. It never
// trusts a persisted READY without checking the container.
func (s *Service) Reconcile(ctx context.Context, x *sessions.Session) (*sessions.Session, error) {
	if !x.State.Active() && x.State != sessions.Unknown {
		return x, nil
	}
	if x.State.Executing() {
		if s.IsRunningHere(x.ID) || pidAlive(x.RunnerPID) {
			return x, nil
		}
		x.Error = zerr.New(zerr.CodeCancelled, string(x.State), "execution was interrupted (the ZEROSETUP process exited)")
		if b, err := s.Backend(ctx); err == nil {
			if owned, err := b.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelSession: x.ID}); err == nil {
				for _, c := range owned.Containers {
					_ = b.Remove(ctx, c.ID)
				}
			}
		}
		x.State = sessions.Unknown
		return x, s.transition(ctx, x, sessions.Failed, "execution interrupted; leftover containers removed")
	}
	b, err := s.Backend(ctx)
	if err != nil {
		if x.State != sessions.Unknown {
			x.State = sessions.Unknown
			_ = s.Store.Save(ctx, x)
		}
		return x, nil
	}
	st, err := b.Inspect(ctx, x.ContainerID)
	switch {
	case err != nil && docker.IsNotFound(err), x.ContainerID == "":
		x.Error = zerr.New(zerr.CodeContainer, "reconcile", "application container no longer exists")
		if x.State != sessions.Unknown && !sessions.CanTransition(x.State, sessions.Failed) {
			x.State = sessions.Unknown
		}
		return x, s.transition(ctx, x, sessions.Failed, "container missing")
	case err != nil:
		return x, nil
	case st.Running && x.State == sessions.Unknown:
		return x, s.transition(ctx, x, sessions.Ready, "container running again")
	case !st.Running && (x.State == sessions.Ready || x.State == sessions.Unknown):
		msg := fmt.Sprintf("application container exited (code %d)", st.ExitCode)
		if st.OOMKilled {
			msg += "; killed for exceeding its memory limit"
		}
		x.Error = zerr.New(zerr.CodeContainer, "reconcile", msg)
		if x.State == sessions.Ready {
			x.State = sessions.Unknown
		}
		return x, s.transition(ctx, x, sessions.Stopped, msg)
	}
	return x, nil
}

// List returns sessions reconciled against the engine.
func (s *Service) List(ctx context.Context, all bool) ([]*sessions.Session, error) {
	list, err := s.Store.List(ctx, all, 200)
	if err != nil {
		return nil, err
	}
	for i, x := range list {
		if y, err := s.Reconcile(ctx, x); err == nil {
			list[i] = y
		}
	}
	return list, nil
}

// Status returns a reconciled session.
func (s *Service) Status(ctx context.Context, sid string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return s.Reconcile(ctx, x)
}

// LogStreams lists available log streams for a session.
func (s *Service) LogStreams(ctx context.Context, sid string) ([]string, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, st := range loadFacts(x).Steps {
		if !st.Skipped {
			out = append(out, st.ID)
		}
	}
	if x.ContainerID != "" {
		out = append(out, "app")
	}
	return out, nil
}

// Logs writes a log stream. stream "app" (default) reads the application
// container; any other value reads that step's log file from the latest attempt.
func (s *Service) Logs(ctx context.Context, sid, stream string, follow bool, tail string, w io.Writer) error {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return err
	}
	if stream == "" || stream == "app" {
		if x.ContainerID == "" {
			return zerr.New(zerr.CodeContainer, "logs", "this session has no application container (it may have failed before starting)").
				WithSuggestion("List step logs with `zerosetup logs " + sid + " --step <id>`; steps: " + strings.Join(stepIDs(x), ", "))
		}
		b, err := s.Backend(ctx)
		if err != nil {
			return err
		}
		if err := b.Logs(ctx, x.ContainerID, follow, tail, w, w); err != nil {
			if docker.IsNotFound(err) {
				return zerr.New(zerr.CodeContainer, "logs", "the application container no longer exists")
			}
			return err
		}
		return nil
	}
	for _, st := range loadFacts(x).Steps {
		if st.ID == stream && st.LogFile != "" {
			f, err := os.Open(st.LogFile)
			if err != nil {
				return zerr.Wrap(err, zerr.CodeStore, "logs", "step log not found")
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		}
	}
	return zerr.New(zerr.CodeContainer, "logs", fmt.Sprintf("no log stream %q; available: app, %s", stream, strings.Join(stepIDs(x), ", ")))
}

func stepIDs(x *sessions.Session) []string {
	var ids []string
	for _, st := range loadFacts(x).Steps {
		if !st.Skipped {
			ids = append(ids, st.ID)
		}
	}
	return ids
}

// Stats samples the application container's resource usage.
func (s *Service) Stats(ctx context.Context, sid string) (*runtime.Stats, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if x.State != sessions.Ready || x.ContainerID == "" {
		return nil, zerr.New(zerr.CodeIllegalState, "metrics", "resource usage is only available while the session is READY")
	}
	b, err := s.Backend(ctx)
	if err != nil {
		return nil, err
	}
	return b.Stats(ctx, x.ContainerID)
}

// CacheStatus describes shared package caches.
type CacheStatus struct {
	Volumes     []string `json:"volumes"`
	SourceDirs  int      `json:"sourceCheckouts"`
	SourceBytes int64    `json:"sourceBytes"`
	SourceRoot  string   `json:"sourceRoot"`
}

// Cache reports cache usage.
func (s *Service) Cache(ctx context.Context) (*CacheStatus, error) {
	cs := &CacheStatus{SourceRoot: s.Paths.Sources}
	_ = filepath.WalkDir(s.Paths.Sources, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == completeMarker {
			cs.SourceDirs++
		}
		if d.Type().IsRegular() {
			if i, err := d.Info(); err == nil {
				cs.SourceBytes += i.Size()
			}
		}
		return nil
	})
	if b, err := s.Backend(ctx); err == nil {
		if owned, err := b.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelRole: "cache"}); err == nil {
			cs.Volumes = owned.Volumes
			sort.Strings(cs.Volumes)
		}
	}
	return cs, nil
}

const completeMarker = ".zerosetup-source.json"

// CleanCache removes ZEROSETUP-owned package cache volumes (only those
// labelled role=cache) and, if sources is true, the source checkout cache.
func (s *Service) CleanCache(ctx context.Context, sources bool) (*CacheStatus, error) {
	removed := &CacheStatus{SourceRoot: s.Paths.Sources}
	b, err := s.Backend(ctx)
	if err == nil {
		owned, err := b.ListOwned(ctx, map[string]string{runtime.LabelManaged: "true", runtime.LabelRole: "cache"})
		if err != nil {
			return nil, err
		}
		for _, v := range owned.Volumes {
			if err := b.RemoveVolume(ctx, v); err != nil {
				return removed, zerr.Wrap(err, zerr.CodeCleanup, "cache", "cache volume "+v+" is in use by a running session; stop it first")
			}
			removed.Volumes = append(removed.Volumes, v)
		}
	}
	if sources {
		cs, _ := s.Cache(ctx)
		removed.SourceDirs, removed.SourceBytes = cs.SourceDirs, cs.SourceBytes
		if err := os.RemoveAll(s.Paths.Sources); err != nil {
			return removed, zerr.Wrap(err, zerr.CodeCleanup, "cache", "cannot remove source cache")
		}
	}
	return removed, nil
}
