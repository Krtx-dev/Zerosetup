package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"sort"

	"github.com/zerosetup/zerosetup/internal/passport"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/policy"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// ReplayCheck is one compatibility check between a passport and today.
type ReplayCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

// ReplayReport is the outcome of validating a passport for replay. Replay
// never claims identical results; it reports what matches and what does not.
type ReplayReport struct {
	Checks     []ReplayCheck     `json:"checks"`
	Reproduced bool              `json:"planReproduced"`
	PlanHash   string            `json:"planHash,omitempty"`
	PlanDiff   []string          `json:"planDiff,omitempty"`
	Session    *sessions.Session `json:"session,omitempty"`
	Inspection *InspectResult    `json:"-"`
	CanRun     bool              `json:"canRun"`
}

// ReplayOptions configures Replay.
type ReplayOptions struct {
	Source        string // required for passports of local sources
	CreateSession bool   // create an AWAITING_APPROVAL session if checks pass
	CheckImages   bool   // pull image tags to compare digests (needs Docker)
}

// Replay validates a passport against the current world and optionally
// prepares a session that re-runs the same plan (still requiring approval).
func (s *Service) Replay(ctx context.Context, data []byte, o ReplayOptions) (*ReplayReport, error) {
	var p passport.Passport
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&p); err != nil {
		return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "replay", "passport is not valid JSON")
	}
	if err := passport.Validate(&p); err != nil {
		return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "replay", "passport failed validation")
	}
	rep := &ReplayReport{}
	add := func(name, status, f string, a ...any) {
		rep.Checks = append(rep.Checks, ReplayCheck{Name: name, Status: status, Detail: fmt.Sprintf(f, a...)})
	}
	var ov planner.Overrides
	if len(p.Execution.OverridesDetail) > 0 {
		if err := json.Unmarshal(p.Execution.OverridesDetail, &ov); err != nil {
			return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "replay", "passport overrides are invalid")
		}
		add("overrides", "ok", "using the %d recorded override(s)", len(p.Execution.Overrides))
	} else {
		add("overrides", "warn", "passport %s does not record override details; replaying with none", p.SchemaVersion)
	}

	req := InspectRequest{Overrides: ov}
	switch p.Source.Kind {
	case "github":
		if p.Source.Commit == "" {
			return nil, zerr.New(zerr.CodePlanInvalid, "replay", "GitHub passport has no commit")
		}
		req.Source = "https://" + p.Source.Repository
		if p.Source.Subdir != "" {
			req.Source += "/tree/" + p.Source.Commit + "/" + p.Source.Subdir
		} else {
			req.Ref = p.Source.Commit
		}
	case "local":
		if o.Source == "" {
			return nil, zerr.New(zerr.CodeSourceInvalid, "replay", "this passport describes a local directory").
				WithSuggestion("Pass --source <dir>; its manifests must match the passport's content hash.")
		}
		req.Source = o.Source
	}
	if o.Source != "" && p.Source.Kind == "github" {
		req.Source, req.Ref = o.Source, ""
	}

	res, err := s.Inspect(ctx, req)
	if err != nil {
		add("source", "fail", "cannot inspect %s: %v", req.Source, err)
		return rep, nil
	}
	rep.Inspection = res
	rep.PlanHash = res.PlanHash
	if p.Source.Commit != "" {
		if res.Plan.Source.Commit == p.Source.Commit {
			add("commit", "ok", "commit %s is available", p.Source.Commit[:12])
		} else {
			add("commit", "fail", "commit is %q, passport recorded %s", res.Plan.Source.Commit, p.Source.Commit)
		}
	}
	if res.Plan.Source.ContentHash == p.Source.ContentHash {
		add("manifests", "ok", "manifest content hash matches")
	} else {
		add("manifests", "fail", "manifest content hash differs (%s vs recorded %s)", short19(res.Plan.Source.ContentHash), short19(p.Source.ContentHash))
	}
	now := map[string]string{}
	for _, m := range res.Inspection.Manifests {
		now[m.File] = m.SHA256
	}
	for _, m := range p.Dependencies.Manifests {
		if !m.Lockfile {
			continue
		}
		switch cur, ok := now[m.File]; {
		case !ok:
			add("lockfile "+m.File, "fail", "missing")
		case cur != m.SHA256:
			add("lockfile "+m.File, "fail", "hash differs")
		default:
			add("lockfile "+m.File, "ok", "identical")
		}
	}
	if !p.Dependencies.Locked {
		add("dependency pinning", "warn", "the original run had no lockfile; dependency versions may differ")
	}

	rep.Reproduced = res.PlanHash == p.Execution.PlanHash
	if rep.Reproduced {
		add("plan", "ok", "the regenerated plan is byte-identical (%s)", ps.ShortHash(res.PlanHash))
	} else {
		rep.PlanDiff = planDiff(p.Plan, res.Plan)
		add("plan", "warn", "the regenerated plan differs from the recorded one in: %v", rep.PlanDiff)
	}
	if v := policy.Check(res.Plan); len(v) > 0 {
		add("policy", "fail", "%s", v[0].Message)
	}

	if p.Platform.EngineArch != "" {
		if eng, err := s.backendInfo(ctx); err == nil {
			if eng.Arch == p.Platform.EngineArch {
				add("platform", "ok", "engine architecture %s matches", eng.Arch)
			} else {
				add("platform", "warn", "engine architecture is %s, recorded %s: images and native dependencies will differ", eng.Arch, p.Platform.EngineArch)
			}
		} else {
			add("platform", "warn", "container engine unavailable; host is %s/%s, recorded engine %s", goruntime.GOOS, goruntime.GOARCH, p.Platform.EngineArch)
		}
	}
	if o.CheckImages && p.Environment.BaseImageDigest != "" {
		if b, err := s.Backend(ctx); err == nil {
			if img, err := b.EnsureImage(ctx, p.Environment.BaseImage, nil); err == nil {
				if img.Digest == p.Environment.BaseImageDigest {
					add("base image", "ok", "%s still resolves to the recorded digest", p.Environment.BaseImage)
				} else {
					add("base image", "warn", "%s now resolves to %s (recorded %s); the runtime may differ", p.Environment.BaseImage, img.Digest, p.Environment.BaseImageDigest)
				}
			} else {
				add("base image", "warn", "cannot check image: %v", err)
			}
		}
	}

	rep.CanRun = true
	for _, c := range rep.Checks {
		if c.Status == "fail" {
			rep.CanRun = false
		}
	}
	if rep.CanRun && o.CreateSession {
		x, err := s.CreateSession(ctx, req, res)
		if err != nil {
			return rep, err
		}
		rep.Session = x
	}
	sort.SliceStable(rep.Checks, func(i, j int) bool { return rank(rep.Checks[i].Status) > rank(rep.Checks[j].Status) })
	return rep, nil
}

func rank(s string) int {
	switch s {
	case "fail":
		return 2
	case "warn":
		return 1
	}
	return 0
}

func short19(s string) string {
	if len(s) > 19 {
		return s[:19]
	}
	return s
}

// planDiff lists top-level plan fields whose canonical JSON differs.
func planDiff(recorded json.RawMessage, now *ps.Plan) []string {
	var a map[string]json.RawMessage
	if json.Unmarshal(recorded, &a) != nil {
		return []string{"(recorded plan unreadable)"}
	}
	nb, _ := ps.Canonical(now)
	var b map[string]json.RawMessage
	_ = json.Unmarshal(nb, &b)
	var out []string
	for k, v := range b {
		if !bytes.Equal(compact(a[k]), compact(v)) {
			out = append(out, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func compact(b []byte) []byte {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return b
	}
	return buf.Bytes()
}
