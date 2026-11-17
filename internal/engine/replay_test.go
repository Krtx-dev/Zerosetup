package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/sessions"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	for _, f := range []string{"plan-1.1.json", "passport-1.1.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "schemas", f))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://zerosetup.dev/schemas/"+f, doc); err != nil {
			t.Fatal(err)
		}
	}
	sch, err := c.Compile("https://zerosetup.dev/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func validateJSON(t *testing.T, sch *jsonschema.Schema, b []byte) {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(v); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}
}

func TestGeneratedArtifactsMatchSchemas(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFake("ok"))
	planSch := compileSchema(t, "plan-1.1.json")
	for _, fx := range []string{"vite", "express", "fastapi", "flask", "django", "nextjs", "monorepo-npm", "missing-start-script"} {
		res, err := s.Inspect(ctx, InspectRequest{Source: fixtureDir(fx), Overrides: planner.Overrides{Services: []string{"postgres"}}})
		if err != nil {
			t.Fatalf("%s: %v", fx, err)
		}
		b, _ := ps.Canonical(res.Plan)
		validateJSON(t, planSch, b)
	}
	x := createSession(t, s, "express", InspectRequest{Overrides: planner.Overrides{Services: []string{"redis"}, Env: map[string]string{"A": "b"}}})
	s.Approve(ctx, x.ID, x.PlanHash)
	if _, err := s.Execute(ctx, x.ID, nil); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Passport(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	validateJSON(t, compileSchema(t, "passport-1.1.json"), b)

	// A secret value in a passport must be rejected by the schema.
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	env := doc["environment"].(map[string]any)
	env["environmentVariables"] = []any{map[string]any{"name": "TOKEN", "secret": true, "value": "leak", "kind": "USER_OVERRIDE"}}
	bad, _ := json.Marshal(doc)
	v, _ := jsonschema.UnmarshalJSON(bytes.NewReader(bad))
	if compileSchema(t, "passport-1.1.json").Validate(v) == nil {
		t.Fatal("schema accepted a secret value")
	}
}

func TestReplay(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFake("ok"))
	ov := planner.Overrides{Port: 3001, Env: map[string]string{"GREETING": "hi"}, Services: []string{"postgres"}}
	x := createSession(t, s, "express", InspectRequest{Overrides: ov})
	s.Approve(ctx, x.ID, x.PlanHash)
	if _, err := s.Execute(ctx, x.ID, nil); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Passport(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replay(ctx, b, ReplayOptions{}); err == nil {
		t.Fatal("local passports must require --source")
	}
	rep, err := s.Replay(ctx, b, ReplayOptions{Source: fixtureDir("express"), CreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Reproduced || !rep.CanRun || rep.PlanHash != x.PlanHash {
		t.Fatalf("expected an identical plan: %+v", rep)
	}
	if rep.Session == nil || rep.Session.State != sessions.AwaitingApproval || rep.Session.ApprovedAt != nil {
		t.Fatal("replay must create a session that still needs approval")
	}
	// Different manifests must fail the replay, not be glossed over.
	rep, err = s.Replay(ctx, b, ReplayOptions{Source: fixtureDir("express-yarn"), CreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CanRun || rep.Session != nil || rep.Reproduced {
		t.Fatalf("mismatched source must not be replayable: %+v", rep.Checks)
	}
	if len(rep.PlanDiff) == 0 {
		t.Fatal("plan differences must be listed")
	}
}
