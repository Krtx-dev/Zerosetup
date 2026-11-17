package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

func TestSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	x := &sessions.Session{ID: sessions.NewID(), State: sessions.Ready, Source: "./x", Repository: "github.com/a/b", PlanHash: "sha256:1",
		URL: "http://127.0.0.1:4317", HostPort: 4317, ApprovedAt: &now, Facts: []byte(`{"k":1}`),
		Milestones: map[sessions.Milestone]time.Time{sessions.MilestoneHTTPReady: now},
		Error:      zerr.Wrap(errFake("boom"), zerr.CodeNotReady, "verify", "not ready").WithSuggestion("retry")}
	if err := st.Save(ctx, x); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != x.State || got.URL != x.URL || got.HostPort != 4317 || !got.ApprovedAt.Equal(now) || string(got.Facts) != `{"k":1}` {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Error == nil || got.Error.Code != zerr.CodeNotReady || got.Error.Suggestion != "retry" || got.Error.CauseText() != "boom" {
		t.Fatalf("error not persisted: %+v", got.Error)
	}
	if !got.Milestones[sessions.MilestoneHTTPReady].Equal(now) {
		t.Fatal("milestones not persisted")
	}
	x.State = sessions.Removed
	st.Save(ctx, x)
	if l, _ := st.List(ctx, false, 10); len(l) != 0 {
		t.Fatal("removed sessions must be hidden by default")
	}
	if l, _ := st.List(ctx, true, 10); len(l) != 1 {
		t.Fatal("removed sessions must be listed with all=true")
	}
	if _, err := st.Get(ctx, "zs00000000"); !zerr.HasCode(err, zerr.CodeSessionNotFound) {
		t.Fatalf("missing session: %v", err)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func TestEventsAndPlans(t *testing.T) {
	ctx := context.Background()
	st, _ := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	for i := 0; i < 5; i++ {
		e := &sessions.Event{SessionID: "zs00000001", Type: "log", Message: "m", Data: map[string]any{"i": i}}
		if err := st.AppendEvent(ctx, e); err != nil || e.Seq == 0 {
			t.Fatal(err)
		}
	}
	st.AppendEvent(ctx, &sessions.Event{SessionID: "zs00000002", Type: "x", Message: "other"})
	evs, _ := st.Events(ctx, "zs00000001", 2, 100)
	if len(evs) != 3 || evs[0].Seq != 3 {
		t.Fatalf("events after seq 2: %+v", evs)
	}
	if err := st.SavePlan(ctx, "sha256:a", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	st.SavePlan(ctx, "sha256:a", []byte(`{"a":2}`)) // idempotent: first write wins
	if b, _ := st.GetPlan(ctx, "sha256:a"); string(b) != `{"a":1}` {
		t.Fatalf("plan content changed: %s", b)
	}
	st.RecordMetric(ctx, "zs00000001", 1, "install", 1500*time.Millisecond)
	if ms, _ := st.Metrics(ctx, "zs00000001"); len(ms) != 1 || ms[0].Millis != 1500 {
		t.Fatalf("metrics: %+v", ms)
	}
}
