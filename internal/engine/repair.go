package engine

import (
	"context"
	"fmt"
	"strconv"

	"github.com/zerosetup/zerosetup/internal/diagnostics"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// MaxRepairs bounds repair attempts per session to avoid uncontrolled
// trial-and-error loops.
const MaxRepairs = 3

// Diagnostics returns the latest diagnostics for a session.
func (s *Service) Diagnostics(ctx context.Context, sid string) ([]diagnostics.Diagnostic, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return loadFacts(x).Diagnostics, nil
}

// RepairOutcome describes an applied (not yet executed) repair.
type RepairOutcome struct {
	Session *sessions.Session  `json:"session"`
	Repair  diagnostics.Repair `json:"repair"`
	OldHash string             `json:"oldPlanHash"`
	NewHash string             `json:"newPlanHash"`
	Plan    *ps.Plan           `json:"plan"`
}

func applyChange(ov planner.Overrides, c diagnostics.Change) (planner.Overrides, error) {
	switch c.Override {
	case "port":
		p, err := strconv.Atoi(c.To)
		if err != nil {
			return ov, err
		}
		ov.Port = p
	case "env":
		m := map[string]string{}
		for k, v := range ov.Env {
			m[k] = v
		}
		m[c.Key] = c.To
		ov.Env = m
	case "memoryMB":
		v, err := strconv.ParseInt(c.To, 10, 64)
		if err != nil {
			return ov, err
		}
		ov.MemoryMB = v
	case "readyTimeoutSec":
		v, err := strconv.Atoi(c.To)
		if err != nil {
			return ov, err
		}
		ov.ReadyTimeoutSec = v
	default:
		return ov, fmt.Errorf("unsupported repair change %q", c.Override)
	}
	return ov, nil
}

// ApplyRepair turns a proposed repair into a new plan via overrides. The
// repository is never modified. The new plan requires fresh approval.
func (s *Service) ApplyRepair(ctx context.Context, sid, repairID string) (*RepairOutcome, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if x.State != sessions.Failed {
		return nil, zerr.New(zerr.CodeIllegalState, "repair", "repairs apply to FAILED sessions; this one is "+string(x.State))
	}
	f := loadFacts(x)
	if len(f.RepairsApplied) >= MaxRepairs {
		return nil, zerr.New(zerr.CodeIllegalState, "repair", fmt.Sprintf("repair limit reached (%d per session)", MaxRepairs)).
			WithSuggestion("Inspect the logs manually; automatic repair attempts are bounded on purpose.")
	}
	var rep *diagnostics.Repair
	for _, d := range f.Diagnostics {
		if d.Repair != nil && d.Repair.ID == repairID {
			rep = d.Repair
		}
	}
	if rep == nil {
		return nil, zerr.New(zerr.CodePlanInvalid, "repair", "no repair "+strconv.Quote(repairID)+" is proposed for this session")
	}
	ov := f.Overrides
	for _, c := range rep.Changes {
		if ov, err = applyChange(ov, c); err != nil {
			return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "repair", "cannot apply repair")
		}
	}
	res, err := s.Inspect(ctx, InspectRequest{Source: f.Input, Ref: f.Ref, Overrides: ov})
	if err != nil {
		return nil, err
	}
	if err := enforce(res); err != nil {
		return nil, err
	}
	planJSON, err := ps.Canonical(res.Plan)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SavePlan(ctx, res.PlanHash, planJSON); err != nil {
		return nil, err
	}
	old := f.Overrides
	f.PrevOverrides = &old
	f.Overrides = ov
	f.RepairsApplied = append(f.RepairsApplied, rep.ID)
	out := &RepairOutcome{Repair: *rep, OldHash: x.PlanHash, NewHash: res.PlanHash, Plan: res.Plan}
	x.PrevPlanHash, x.PlanHash, x.ApprovedAt = x.PlanHash, res.PlanHash, nil
	saveFacts(x, f)
	if err := s.transition(ctx, x, sessions.AwaitingApproval, fmt.Sprintf("repair %q applied to the plan (%s → %s); awaiting approval",
		rep.ID, ps.ShortHash(out.OldHash), ps.ShortHash(out.NewHash))); err != nil {
		return nil, err
	}
	out.Session = x
	return out, nil
}

// RevertRepair restores the plan that was in effect before the last repair.
func (s *Service) RevertRepair(ctx context.Context, sid string) (*sessions.Session, error) {
	x, err := s.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	f := loadFacts(x)
	if x.PrevPlanHash == "" || f.PrevOverrides == nil {
		return nil, zerr.New(zerr.CodeIllegalState, "repair", "there is no repair to revert")
	}
	if x.State.Active() {
		return nil, zerr.New(zerr.CodeIllegalState, "repair", "stop the session before reverting")
	}
	x.PlanHash, x.PrevPlanHash, x.ApprovedAt = x.PrevPlanHash, "", nil
	f.Overrides, f.PrevOverrides = *f.PrevOverrides, nil
	if n := len(f.RepairsApplied); n > 0 {
		f.RepairsApplied = f.RepairsApplied[:n-1]
	}
	saveFacts(x, f)
	if x.State != sessions.AwaitingApproval {
		if err := s.transition(ctx, x, sessions.AwaitingApproval, "repair reverted; previous plan restored"); err != nil {
			return nil, err
		}
	} else if err := s.Store.Save(ctx, x); err != nil {
		return nil, err
	}
	return x, nil
}
