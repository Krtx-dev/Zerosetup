package engine

import (
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"time"

	"github.com/zerosetup/zerosetup/internal/passport"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Passport builds a session's reproducibility passport.
func (s *Service) Passport(ctx context.Context, sid string) ([]byte, *passport.Passport, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, nil, err
	}
	plan, err := s.Plan(ctx, x)
	if err != nil {
		return nil, nil, err
	}
	planJSON, err := ps.Canonical(plan)
	if err != nil {
		return nil, nil, err
	}
	f := loadFacts(x)
	p := &passport.Passport{
		SchemaVersion: passport.SchemaVersion, Kind: "zerosetup.passport",
		Generator: passport.Generator{Name: "zerosetup", Version: Version},
		Session: passport.SessionInfo{ID: x.ID, State: string(x.State), Attempt: x.Attempt,
			CreatedAt: x.CreatedAt.UTC().Format(time.RFC3339), ExportedAt: time.Now().UTC().Format(time.RFC3339)},
		Source: passport.Source{Kind: plan.Source.Kind, Repository: plan.Source.Repository, URL: plan.Source.URL, Ref: plan.Source.Ref,
			Commit: plan.Source.Commit, Subdir: plan.Source.Subdir, ContentHash: plan.Source.ContentHash},
		Environment: passport.Environment{Runtime: plan.Stack.Runtime, RuntimeVersion: plan.Stack.RuntimeVersion, Framework: plan.Stack.Framework,
			PackageManager: plan.Stack.PackageManager, PackageManagerVersion: plan.Stack.PackageManagerVersion, BaseImage: plan.Image.Reference},
		Execution: passport.Execution{PlanSchemaVersion: plan.SchemaVersion, PlanHash: x.PlanHash, Start: plan.Start.Argv, ContainerPort: plan.PrimaryPort(),
			Resources: map[string]any{"cpus": plan.Resources.CPUs, "memoryMB": plan.Resources.MemoryMB, "pidsLimit": plan.Resources.PidsLimit},
			Security: map[string]any{"user": fmt.Sprintf("%d:%d", ps.ContainerUID, ps.ContainerUID), "capabilities": "all dropped",
				"noNewPrivileges": true, "readonlyRootfs": true, "hostMounts": false, "publishHost": plan.Network.PublishHost,
				"outboundNetworkFiltered": plan.Network.OutboundFiltered},
			Overrides: plan.Overrides, RepairsApplied: f.RepairsApplied, Status: string(x.State)},
		Verification: passport.Verification{Milestones: map[string]string{}},
		Platform:     passport.Platform{HostOS: goruntime.GOOS, HostArch: goruntime.GOARCH},
		Exclusions: []string{
			"secret environment variable values (names only)",
			"local .env files (never copied into the environment)",
			"credentials, tokens and personal paths (home directory names are replaced with <user>)",
		},
		Plan: planJSON,
	}
	if b, err := json.Marshal(f.Overrides); err == nil {
		p.Execution.OverridesDetail = b
	}
	for _, sv := range f.Services {
		p.Execution.Services = append(p.Execution.Services, passport.ServiceInfo{Kind: sv.Kind, Image: sv.Image, Digest: sv.Digest})
	}
	if p.Execution.Overrides == nil {
		p.Execution.Overrides = []string{}
	}
	if p.Execution.RepairsApplied == nil {
		p.Execution.RepairsApplied = []string{}
	}
	if x.ApprovedAt != nil {
		p.Session.ApprovedAt = x.ApprovedAt.UTC().Format(time.RFC3339)
	}
	if f.Image != nil {
		p.Environment.BaseImageDigest, p.Environment.BaseImageID = f.Image.Digest, f.Image.ID
		p.Environment.ImageOS, p.Environment.ImageArchitecture = f.Image.OS, f.Image.Arch
	}
	p.Environment.EnvironmentVariables = []passport.EnvVar{}
	for _, e := range plan.Env {
		v := passport.EnvVar{Name: e.Name, Secret: e.Secret, Kind: string(e.Kind)}
		if !e.Secret {
			v.Value = e.Value
		}
		p.Environment.EnvironmentVariables = append(p.Environment.EnvironmentVariables, v)
	}
	p.Dependencies.Manifests = []passport.FileHash{}
	for _, m := range f.Manifests {
		p.Dependencies.Manifests = append(p.Dependencies.Manifests, passport.FileHash{File: m.File, SHA256: m.SHA256, Lockfile: m.Lock})
		if m.Lock {
			p.Dependencies.Locked = true
		}
	}
	recs := map[string]StepRecord{}
	for _, r := range f.Steps {
		recs[r.ID] = r
	}
	p.Execution.Steps = []passport.Step{}
	for _, st := range plan.Steps {
		ps := passport.Step{ID: st.ID, Name: st.Name, Stage: st.Stage, Argv: st.Command.Argv}
		if r, ok := recs[st.ID]; ok {
			if r.Skipped {
				ps.Skipped = true
			} else {
				code := r.ExitCode
				ps.ExitCode, ps.DurationMs = &code, r.DurationMs
			}
		}
		p.Execution.Steps = append(p.Execution.Steps, ps)
	}
	if f.Cache != nil {
		p.Execution.Cache = map[string]any{"volume": f.Cache.Volume, "warm": f.Cache.VolumeExisted, "dependencyKey": f.Cache.DepsKey,
			"dependenciesReused": f.Cache.DepsReused, "reason": f.Cache.Reason}
	}
	for m, t := range x.Milestones {
		p.Verification.Milestones[string(m)] = t.UTC().Format(time.RFC3339Nano)
	}
	if r := f.Readiness; r != nil {
		p.Verification.Readiness = map[string]any{"path": r.Path, "attempts": r.Attempts, "durationMs": r.DurationMs, "lastStatus": r.LastStatus,
			"httpReady": r.HTTPReady, "healthy": r.Healthy, "listeners": r.Listeners, "failedReason": r.FailedReason}
	}
	p.Detection.Decisions = []passport.Decision{}
	for _, d := range plan.Decisions {
		pd := passport.Decision{Field: d.Field, Value: d.Value, Kind: string(d.Kind), Certainty: string(d.Certainty), Reason: d.Reason}
		for _, e := range d.Evidence {
			pd.Evidence = append(pd.Evidence, e.File+": "+e.Detail)
		}
		p.Detection.Decisions = append(p.Detection.Decisions, pd)
	}
	p.Detection.Warnings = []string{}
	for _, w := range plan.Warnings {
		p.Detection.Warnings = append(p.Detection.Warnings, w.Code+": "+w.Message)
	}
	p.Detection.Conflicts = []string{}
	for _, c := range plan.Conflicts {
		p.Detection.Conflicts = append(p.Detection.Conflicts, fmt.Sprintf("%s: %v → %s", c.Field, c.Signals, c.Resolution))
	}
	if f.Engine != nil {
		p.Platform.Engine, p.Platform.EngineVersion = f.Engine.Backend, f.Engine.ServerVersion
		p.Platform.EngineOS, p.Platform.EngineArch, p.Platform.CgroupVersion = f.Engine.OS, f.Engine.Arch, f.Engine.CgroupVersion
	}
	p.Timings = []passport.Timing{}
	if ms, err := s.Store.Metrics(ctx, sid); err == nil {
		for _, m := range ms {
			p.Timings = append(p.Timings, passport.Timing{Attempt: m.Attempt, Name: m.Name, Millis: m.Millis})
		}
	}
	p.Diagnostics = []passport.Diagnostic{}
	for _, d := range f.Diagnostics {
		p.Diagnostics = append(p.Diagnostics, passport.Diagnostic{ID: d.ID, Severity: d.Severity, Summary: d.Summary})
	}
	if err := passport.Validate(p); err != nil {
		return nil, nil, zerr.Wrap(err, zerr.CodeInternal, "export", "generated passport failed validation")
	}
	b, err := passport.Marshal(p)
	if err != nil {
		return nil, nil, err
	}
	// Secrets are never stored, so there are no values to scrub here beyond
	// personal paths; Scrub still verifies the result.
	b, err = passport.Scrub(b, nil)
	if err != nil {
		return nil, nil, zerr.Wrap(err, zerr.CodeInternal, "export", "passport scrubbing failed")
	}
	return b, p, nil
}
