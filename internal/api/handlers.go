package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "version": engine.Version})
}

func (s *Server) doctor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"checks": s.svc.Doctor(r.Context())})
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	var req engine.InspectRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.Inspect(r.Context(), req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, inspectView(res))
}

// inspectView is the API representation of an inspection.
func inspectView(res *engine.InspectResult) map[string]any {
	return map[string]any{
		"planHash": res.PlanHash, "planShortHash": ps.ShortHash(res.PlanHash), "plan": res.Plan,
		"policyViolations": res.Violations, "detectors": res.Detected, "services": res.Services,
		"source": res.Resolved, "manifests": res.Inspection.Manifests, "files": res.Inspection.Report.Files, "timingMs": res.Timing,
	}
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	b, err := s.svc.PlanJSON(r.Context(), r.PathValue("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req engine.InspectRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.Inspect(r.Context(), req)
	if err != nil {
		fail(w, err)
		return
	}
	if !res.Plan.Compatibility.Runnable {
		writeErr(w, http.StatusUnprocessableEntity, zerr.New(zerr.CodeUnsupportedStack, "inspect", "repository is not runnable by ZEROSETUP").
			WithDetail("%s", strings.Join(res.Plan.Compatibility.Reasons, "; ")))
		return
	}
	x, err := s.svc.CreateSession(r.Context(), req, res)
	if err != nil {
		fail(w, err)
		return
	}
	v := inspectView(res)
	v["session"] = x
	writeJSON(w, 201, v)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.List(r.Context(), r.URL.Query().Get("all") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	if list == nil {
		list = []*sessions.Session{}
	}
	writeJSON(w, 200, map[string]any{"sessions": list})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	x, err := s.svc.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	plan, err := s.svc.Plan(r.Context(), x)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"session": x, "facts": engine.FactsOf(x), "plan": plan,
		"planShortHash": ps.ShortHash(x.PlanHash), "executingHere": s.svc.IsRunningHere(x.ID)})
}

type approveReq struct {
	PlanHash string            `json:"planHash"`
	Secrets  map[string]string `json:"secrets,omitempty"`
}

// runAsync starts a long-running operation detached from the request.
func (s *Server) runAsync(id string, fn func(ctx context.Context)) {
	base := s.baseCtx
	if base == nil {
		base = context.Background()
	}
	go fn(base)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req approveReq
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	x, err := s.svc.Approve(r.Context(), id, req.PlanHash)
	if err != nil {
		fail(w, err)
		return
	}
	secrets := req.Secrets
	s.runAsync(id, func(ctx context.Context) { _, _ = s.svc.Execute(ctx, id, secrets) })
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": map[string]any{"kind": "execute", "sessionId": id,
		"events": fmt.Sprintf("/api/v1/sessions/%s/events", id)}, "session": x})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.svc.Cancel(id) {
		writeErr(w, http.StatusConflict, zerr.New(zerr.CodeIllegalState, "cancel", "session is not executing in this dashboard process"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"cancelled": true})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	x, err := s.svc.Stop(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"session": x})
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req approveReq
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return
		}
	}
	if _, err := s.svc.Get(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.runAsync(id, func(ctx context.Context) { _, _ = s.svc.Restart(ctx, id, req.Secrets) })
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": map[string]any{"kind": "restart", "sessionId": id}})
}

func (s *Server) removeSession(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Remove(r.Context(), r.PathValue("id"))
	if err != nil {
		if res != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"result": res, "error": err.Error()})
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"result": res})
}

type limitedWriter struct {
	w     http.ResponseWriter
	n     int
	limit int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= l.limit {
		return len(p), nil
	}
	if l.n+len(p) > l.limit {
		p = p[:l.limit-l.n]
	}
	l.n += len(p)
	return l.w.Write(p)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tail := q.Get("tail")
	if tail == "" {
		tail = "1000"
	}
	if tail != "all" {
		if _, err := strconv.Atoi(tail); err != nil {
			writeErr(w, 400, zerr.New(zerr.CodePlanInvalid, "api", "tail must be a number or 'all'"))
			return
		}
	}
	stream := q.Get("stream")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.svc.Logs(ctx, r.PathValue("id"), stream, false, tail, &limitedWriter{w: w, limit: 8 << 20}); err != nil {
		fail(w, err)
	}
}

// events streams session events as Server-Sent Events. The handler tails
// the events table so runs started by a separate CLI process are visible.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.svc.Get(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, zerr.New(zerr.CodeInternal, "api", "streaming unsupported"))
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fl.Flush()
	ctx := r.Context()
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	keepalive := time.Now()
	for {
		evs, err := s.svc.Store.Events(ctx, id, after, 500)
		if err != nil {
			return
		}
		for _, e := range evs {
			b, _ := jsonMarshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, b)
			after = e.Seq
		}
		if len(evs) > 0 {
			fl.Flush()
			keepalive = time.Now()
		} else if time.Since(keepalive) > 15*time.Second {
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
			keepalive = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	timings, err := s.svc.Store.Metrics(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"timings": timings}
	if st, err := s.svc.Stats(r.Context(), id); err == nil {
		out["usage"] = st
	} else if ze, ok := zerr.As(err); ok {
		out["usageUnavailable"] = ze.Summary
	}
	writeJSON(w, 200, out)
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	d, err := s.svc.Diagnostics(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"diagnostics": d})
}

func (s *Server) applyRepair(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.ApplyRepair(r.Context(), r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"repair": out.Repair, "oldPlanHash": out.OldHash, "newPlanHash": out.NewHash,
		"newPlanShortHash": ps.ShortHash(out.NewHash), "plan": out.Plan, "session": out.Session})
}

func (s *Server) revertRepair(w http.ResponseWriter, r *http.Request) {
	x, err := s.svc.RevertRepair(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"session": x})
}

func (s *Server) passport(w http.ResponseWriter, r *http.Request) {
	b, _, err := s.svc.Passport(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="zerosetup-passport-`+r.PathValue("id")+`.json"`)
	}
	_, _ = w.Write(b)
}

func (s *Server) cache(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.Cache(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func (s *Server) cleanCache(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.CleanCache(r.Context(), false)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	p := s.svc.Paths
	writeJSON(w, 200, map[string]any{
		"home": p.Home, "database": p.DB, "sourceCache": p.Sources, "sessionLogs": p.Logs, "version": engine.Version,
		"defaults": map[string]any{"cpus": planner.DefaultCPUs, "memoryMB": planner.DefaultMemoryMB, "pidsLimit": planner.DefaultPidsLimit,
			"logMaxSizeMB": planner.DefaultLogMaxSizeMB, "readinessTimeoutSec": planner.DefaultStartTimeoutSec, "previewPorts": "127.0.0.1:4317+"},
		"fixedContainerEnv": engine.FixedEnv(),
	})
}
