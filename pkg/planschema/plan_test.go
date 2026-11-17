package planschema

import (
	"encoding/json"
	"strings"
	"testing"
)

func validPlan() *Plan {
	return &Plan{
		SchemaVersion: SchemaVersion,
		Source:        Source{Kind: "local", ContentHash: "sha256:abc"},
		Stack:         Stack{Runtime: "node", RuntimeVersion: "24"},
		Image:         Image{Reference: "docker.io/library/node:24-slim"},
		Steps:         []Step{{ID: "install-deps", Name: "Install", Stage: StageInstall, Command: Command{Argv: []string{"npm", "ci"}}, TimeoutSec: 60}},
		Start:         Command{Argv: []string{"node", "index.js"}},
		Ports:         []Port{{Name: "http", ContainerPort: 3000, Protocol: "http"}},
		Readiness:     []ReadinessCheck{{Type: "http", Port: 3000, Path: "/", TimeoutSec: 60}},
		Resources:     Resources{CPUs: 1, MemoryMB: 512, PidsLimit: 100, LogMaxSizeMB: 10, StartTimeoutSec: 60},
		Network:       NetworkPolicy{PublishHost: "127.0.0.1"},
		Filesystem:    FilesystemPolicy{ReadonlyRootfs: true},
		Compatibility: Compatibility{Runnable: true, Level: "supported"},
	}
}

func TestHashDeterministicAndRoundTrip(t *testing.T) {
	p := validPlan()
	h1, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := Hash(validPlan())
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s vs %s", h1, h2)
	}
	b, _ := Canonical(p)
	back, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	h3, _ := Hash(back)
	if h1 != h3 {
		t.Fatalf("hash changed after JSON round trip")
	}
	p.Ports[0].ContainerPort = 3001
	h4, _ := Hash(p)
	if h4 == h1 {
		t.Fatal("hash did not change when the plan changed")
	}
	if !strings.HasPrefix(h1, "sha256:") || len(ShortHash(h1)) != 12 {
		t.Fatalf("unexpected hash format %q", h1)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(validPlan()); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	cases := map[string]func(*Plan){
		"workdir escape":      func(p *Plan) { p.Start.Workdir = "../../etc" },
		"absolute workdir":    func(p *Plan) { p.Start.Workdir = "/etc" },
		"bad port":            func(p *Plan) { p.Ports[0].ContainerPort = 70000 },
		"readiness port":      func(p *Plan) { p.Readiness[0].Port = 1 },
		"secret with value":   func(p *Plan) { p.Env = []EnvVar{{Name: "API_KEY", Value: "x", Secret: true}} },
		"bad env name":        func(p *Plan) { p.Env = []EnvVar{{Name: "1BAD"}} },
		"host mounts":         func(p *Plan) { p.Filesystem.HostMounts = true },
		"no limits":           func(p *Plan) { p.Resources.MemoryMB = 0 },
		"empty argv element":  func(p *Plan) { p.Start.Argv = []string{"node", ""} },
		"duplicate step id":   func(p *Plan) { p.Steps = append(p.Steps, p.Steps[0]) },
		"wrong schemaVersion": func(p *Plan) { p.SchemaVersion = "0.9" },
		"incomplete service":  func(p *Plan) { p.Services = []Service{{Kind: "postgres"}} },
	}
	for name, mut := range cases {
		p := validPlan()
		mut(p)
		if err := Validate(p); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestMigrate(t *testing.T) {
	if _, err := Migrate([]byte(`{"schemaVersion":"2.0"}`)); err == nil {
		t.Fatal("future major version must be rejected")
	}
	if _, err := Migrate([]byte(`{}`)); err == nil {
		t.Fatal("missing version must be rejected")
	}
	if _, err := Migrate([]byte(`not json`)); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	b, _ := json.Marshal(validPlan())
	if _, err := Decode(b); err != nil {
		t.Fatalf("current version must decode: %v", err)
	}
	// Unknown fields are rejected (strict decoding).
	if _, err := Decode([]byte(`{"schemaVersion":"1.0","bogus":1}`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}
