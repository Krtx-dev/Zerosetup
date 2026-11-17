// Package detect defines ZEROSETUP's pluggable, deterministic repository
// detectors. Detectors only read files through a Repo; they never execute
// repository code and never call the network or an LLM.
package detect

import (
	"context"
	"path"
	"sort"
	"strings"

	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Repo is the read-only repository view detectors may use.
type Repo interface {
	Exists(rel string) bool
	IsDir(rel string) bool
	ReadFile(rel string, max int64) ([]byte, error)
	Files() []string
	FindByName(maxDepth int, names ...string) []string
}

// Hints are user-provided, validated overrides that detectors honour.
type Hints struct {
	Script         string // package.json script to run instead of the detected one
	RuntimeVersion string // e.g. "22" or "3.12"
	Port           int
	Mode           string // "" or "dev" | "production"
	Workspace      string // npm workspace directory to run, relative to the root
}

// Production reports whether production mode was requested.
func (h Hints) Production() bool { return h.Mode == "production" }

// Result is a single detector's view of how to run the repository.
type Result struct {
	DetectorID   string
	Stack        ps.Stack
	Image        string
	Steps        []ps.Step
	Start        ps.Command
	Port         int
	Env          []ps.EnvVar
	Readiness    ps.ReadinessCheck
	Decisions    []ps.Decision
	Warnings     []ps.Warning
	Conflicts    []ps.Conflict
	Services     []ServiceHint
	Unsupported  []string // reasons the repository cannot run via this detector
	Experimental bool
	// Score orders competing applicable detectors; higher wins.
	Score int
}

// ServiceHint is evidence that the app needs a backing service. Hints are
// reported only; provisioning requires explicit configuration.
type ServiceHint struct {
	Kind     string      `json:"kind"` // postgres | redis
	Evidence ps.Evidence `json:"evidence"`
}

// Runnable reports whether the result can be executed.
func (r *Result) Runnable() bool { return len(r.Unsupported) == 0 && len(r.Start.Argv) > 0 }

// Decide appends a decision.
func (r *Result) Decide(field, value string, kind ps.Kind, c ps.Certainty, reason string, ev ...ps.Evidence) {
	r.Decisions = append(r.Decisions, ps.Decision{Field: field, Value: value, Kind: kind, Certainty: c, Reason: reason, Evidence: ev})
}

// Warn appends a warning.
func (r *Result) Warn(code, msg, file string) {
	r.Warnings = append(r.Warnings, ps.Warning{Code: code, Message: msg, File: file})
}

// Detector is the extension point for language/framework support.
type Detector interface {
	ID() string
	// Priority breaks ties between equally scored results (higher first).
	Priority() int
	// Supports is a cheap check (file existence only).
	Supports(repo Repo) bool
	// Detect performs full analysis. It returns (nil, nil) if, after a
	// closer look, the detector does not apply.
	Detect(ctx context.Context, repo Repo, hints Hints) (*Result, error)
}

// Report is the combined output of all detectors.
type Report struct {
	Results   []*Result           `json:"-"`
	Primary   *Result             `json:"-"`
	Existing  []ps.ExistingConfig `json:"existing"`
	Files     int                 `json:"files"`
	Truncated bool                `json:"truncated"`
}

// Registry holds detectors in registration order.
type Registry struct{ detectors []Detector }

// NewRegistry returns a registry with the given detectors.
func NewRegistry(ds ...Detector) *Registry { return &Registry{detectors: ds} }

// Detectors returns registered detectors.
func (r *Registry) Detectors() []Detector { return r.detectors }

// Run executes every supporting detector and picks a primary result.
func (r *Registry) Run(ctx context.Context, repo Repo, hints Hints) (*Report, error) {
	rep := &Report{Files: len(repo.Files())}
	if t, ok := repo.(interface{ Truncated() bool }); ok {
		rep.Truncated = t.Truncated()
	}
	type scored struct {
		res *Result
		pri int
	}
	var all []scored
	for _, d := range r.detectors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !d.Supports(repo) {
			continue
		}
		res, err := d.Detect(ctx, repo, hints)
		if err != nil {
			return nil, err
		}
		if res == nil {
			continue
		}
		res.DetectorID = d.ID()
		all = append(all, scored{res, d.Priority()})
	}
	sort.SliceStable(all, func(i, j int) bool {
		ri, rj := all[i].res, all[j].res
		if ri.Runnable() != rj.Runnable() {
			return ri.Runnable()
		}
		if ri.Score != rj.Score {
			return ri.Score > rj.Score
		}
		return all[i].pri > all[j].pri
	})
	for _, s := range all {
		rep.Results = append(rep.Results, s.res)
	}
	if len(rep.Results) > 0 {
		rep.Primary = rep.Results[0]
	}
	rep.Existing = AnalyzeExisting(repo)
	return rep, nil
}

// SubRepo views a subdirectory of a repository as its root. Used to run
// detectors against one workspace package of a monorepo.
type SubRepo struct {
	Repo   Repo
	Prefix string
}

func (s SubRepo) p(rel string) string { return s.Prefix + "/" + rel }

func (s SubRepo) Exists(rel string) bool { return s.Repo.Exists(s.p(rel)) }
func (s SubRepo) IsDir(rel string) bool  { return s.Repo.IsDir(s.p(rel)) }
func (s SubRepo) ReadFile(rel string, max int64) ([]byte, error) {
	return s.Repo.ReadFile(s.p(rel), max)
}
func (s SubRepo) Files() []string {
	var out []string
	for _, f := range s.Repo.Files() {
		if rest, ok := strings.CutPrefix(f, s.Prefix+"/"); ok {
			out = append(out, rest)
		}
	}
	return out
}
func (s SubRepo) FindByName(maxDepth int, names ...string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, f := range s.Files() {
		if strings.Count(f, "/") <= maxDepth && want[path.Base(f)] {
			out = append(out, f)
		}
	}
	return out
}
