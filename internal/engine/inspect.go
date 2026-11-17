package engine

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/zerosetup/zerosetup/internal/inspector"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/policy"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/source"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// InspectRequest is the input for Inspect and Plan.
type InspectRequest struct {
	Source    string            `json:"source"`
	Ref       string            `json:"ref,omitempty"`
	Overrides planner.Overrides `json:"overrides"`
}

// InspectResult is a full, non-executing analysis.
type InspectResult struct {
	Resolved   *source.Resolved      `json:"resolved"`
	Inspection *inspector.Inspection `json:"inspection"`
	Plan       *ps.Plan              `json:"plan"`
	PlanHash   string                `json:"planHash"`
	Violations []policy.Violation    `json:"policyViolations"`
	Detected   []DetectorSummary     `json:"detectors"`
	Services   []ServiceSummary      `json:"services"`
	Timing     map[string]int64      `json:"timingMs"`
}

// DetectorSummary is a compact per-detector result.
type DetectorSummary struct {
	ID          string   `json:"id"`
	Framework   string   `json:"framework"`
	Runnable    bool     `json:"runnable"`
	Unsupported []string `json:"unsupported,omitempty"`
}

// ServiceSummary reports a likely backing service (never provisioned).
type ServiceSummary struct {
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Evidence string `json:"evidence"`
}

// Inspect resolves the source, runs detectors and builds a plan. It never
// executes repository code and does not require Docker.
func (s *Service) Inspect(ctx context.Context, req InspectRequest) (*InspectResult, error) {
	t0 := time.Now()
	if err := req.Overrides.Validate(); err != nil {
		return nil, err
	}
	res, err := s.Resolver.Resolve(ctx, req.Source, req.Ref)
	if err != nil {
		return nil, err
	}
	t1 := time.Now()
	in, err := inspector.Inspect(ctx, res, s.Registry, req.Overrides.Hints())
	if err != nil {
		return nil, err
	}
	t2 := time.Now()
	plan, err := planner.Build(in, req.Overrides)
	if err != nil {
		return nil, err
	}
	hash, err := ps.Hash(plan)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeInternal, "plan", "cannot hash plan")
	}
	out := &InspectResult{Resolved: res, Inspection: in, Plan: plan, PlanHash: hash, Violations: append([]policy.Violation{}, policy.Check(plan)...),
		Detected: []DetectorSummary{}, Services: []ServiceSummary{},
		Timing: map[string]int64{"resolve": t1.Sub(t0).Milliseconds(), "inspect": t2.Sub(t1).Milliseconds(), "plan": time.Since(t2).Milliseconds()}}
	for _, r := range in.Report.Results {
		out.Detected = append(out.Detected, DetectorSummary{ID: r.DetectorID, Framework: r.Stack.Framework, Runnable: r.Runnable(), Unsupported: r.Unsupported})
		for _, sv := range r.Services {
			out.Services = append(out.Services, ServiceSummary{Kind: sv.Kind, File: sv.Evidence.File, Evidence: sv.Evidence.Detail})
		}
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Kind < out.Services[j].Kind })
	return out, nil
}

// CreateSession records an inspected plan as a session awaiting approval.
// Plans that violate policy cannot become sessions.
func (s *Service) CreateSession(ctx context.Context, req InspectRequest, r *InspectResult) (*sessions.Session, error) {
	if err := policy.Enforce(r.Plan); err != nil {
		return nil, err
	}
	planJSON, err := ps.Canonical(r.Plan)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SavePlan(ctx, r.PlanHash, planJSON); err != nil {
		return nil, err
	}
	repo := r.Plan.Source.Repository
	if repo == "" {
		repo = r.Resolved.Dir
	}
	x := &sessions.Session{ID: sessions.NewID(), State: sessions.Created, Source: req.Source, Repository: repo,
		Commit: r.Plan.Source.Commit, Framework: r.Plan.Stack.Framework, Runtime: r.Plan.Stack.Runtime + " " + r.Plan.Stack.RuntimeVersion,
		PlanHash: r.PlanHash, PlanJSON: planJSON, Milestones: map[sessions.Milestone]time.Time{}}
	f := &Facts{ZerosetupVersion: Version, Input: req.Source, Ref: req.Ref, SourceDir: r.Resolved.Dir,
		Overrides: req.Overrides, Manifests: r.Inspection.Manifests}
	saveFacts(x, f)
	if err := s.Store.Save(ctx, x); err != nil {
		return nil, err
	}
	s.emit(ctx, x.ID, "state", string(sessions.Created), "session created for "+repo, nil)
	for _, st := range []sessions.State{sessions.Inspecting, sessions.Planned, sessions.AwaitingApproval} {
		if err := s.transition(ctx, x, st, ""); err != nil {
			return nil, err
		}
	}
	s.emit(ctx, x.ID, "stage", "plan", "plan "+ps.ShortHash(r.PlanHash)+" generated", map[string]any{"planHash": r.PlanHash,
		"framework": r.Plan.Stack.Framework, "durationMs": r.Inspection.Duration.Milliseconds()})
	return x, nil
}

// Plan loads a session's plan.
func (s *Service) Plan(ctx context.Context, x *sessions.Session) (*ps.Plan, error) {
	b, err := s.Store.GetPlan(ctx, x.PlanHash)
	if err != nil {
		return nil, err
	}
	return ps.Decode(b)
}

// PlanJSON returns a plan by hash as indented JSON.
func (s *Service) PlanJSON(ctx context.Context, hash string) (json.RawMessage, error) {
	b, err := s.Store.GetPlan(ctx, hash)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func enforce(r *InspectResult) error { return policy.Enforce(r.Plan) }
