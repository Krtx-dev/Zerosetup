package policy

import (
	"testing"

	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func okPlan() *ps.Plan {
	return &ps.Plan{
		Image:         ps.Image{Reference: "docker.io/library/node:24-slim"},
		Steps:         []ps.Step{{Command: ps.Command{Argv: []string{"npm", "ci"}}}},
		Start:         ps.Command{Argv: []string{"node_modules/.bin/vite", "--host", "0.0.0.0"}},
		Ports:         []ps.Port{{ContainerPort: 5173}},
		Resources:     ps.Resources{CPUs: 2, MemoryMB: 2048, PidsLimit: 1024, LogMaxSizeMB: 10},
		Network:       ps.NetworkPolicy{PublishHost: "127.0.0.1"},
		Filesystem:    ps.FilesystemPolicy{ReadonlyRootfs: true},
		Compatibility: ps.Compatibility{Runnable: true},
	}
}

func TestPolicy(t *testing.T) {
	if v := Check(okPlan()); len(v) != 0 {
		t.Fatalf("ok plan rejected: %+v", v)
	}
	bad := map[string]func(*ps.Plan){
		"shell start":      func(p *ps.Plan) { p.Start.Argv = []string{"sh", "-c", "npm start"} },
		"bash by path":     func(p *ps.Plan) { p.Start.Argv = []string{"/bin/bash", "-c", "x"} },
		"env wrapper":      func(p *ps.Plan) { p.Steps[0].Command.Argv = []string{"env", "X=1", "npm"} },
		"arbitrary binary": func(p *ps.Plan) { p.Start.Argv = []string{"curl", "evil"} },
		"bin traversal":    func(p *ps.Plan) { p.Start.Argv = []string{"node_modules/.bin/../../x"} },
		"foreign image":    func(p *ps.Plan) { p.Image.Reference = "docker.io/evil/node:24" },
		"latest image":     func(p *ps.Plan) { p.Image.Reference = "node:latest" },
		"public bind":      func(p *ps.Plan) { p.Network.PublishHost = "0.0.0.0" },
		"claimed egress":   func(p *ps.Plan) { p.Network.OutboundFiltered = true },
		"host mounts":      func(p *ps.Plan) { p.Filesystem.HostMounts = true },
		"writable rootfs":  func(p *ps.Plan) { p.Filesystem.ReadonlyRootfs = false },
		"unlimited memory": func(p *ps.Plan) { p.Resources.MemoryMB = 0 },
		"huge pids":        func(p *ps.Plan) { p.Resources.PidsLimit = 100000 },
		"LD_PRELOAD":       func(p *ps.Plan) { p.Env = []ps.EnvVar{{Name: "LD_PRELOAD", Value: "/x.so"}} },
		"PATH override":    func(p *ps.Plan) { p.Env = []ps.EnvVar{{Name: "PATH", Value: "/evil"}} },
		"secret value":     func(p *ps.Plan) { p.Env = []ps.EnvVar{{Name: "TOKEN", Value: "x", Secret: true}} },
		"not runnable":     func(p *ps.Plan) { p.Compatibility.Runnable = false },
		"foreign service": func(p *ps.Plan) {
			p.Services = []ps.Service{{Kind: "x", Image: "evil/db", User: "1:1", ReadyCommand: []string{"true"}}}
		},
		"root service": func(p *ps.Plan) {
			p.Services = []ps.Service{{Kind: "postgres", Image: "docker.io/library/postgres:17-alpine", User: "0:0", ReadyCommand: []string{"pg_isready"}}}
		},
		"workspace escape": func(p *ps.Plan) { p.Start = ps.Command{Argv: []string{"../../node_modules/.bin/vite"}, Workdir: "web"} },
	}
	for name, mut := range bad {
		p := okPlan()
		mut(p)
		if v := Check(p); len(v) == 0 {
			t.Errorf("%s: expected a violation", name)
		}
		if Enforce(p) == nil {
			t.Errorf("%s: Enforce must fail", name)
		}
	}
}
