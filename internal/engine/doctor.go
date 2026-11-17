package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	"github.com/zerosetup/zerosetup/internal/runtime"
)

// Check is one doctor result.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // ok | warn | fail
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Doctor verifies the host can run ZEROSETUP. Every result reflects a real probe.
func (s *Service) Doctor(ctx context.Context) []Check {
	var out []Check
	plat := goruntime.GOOS + "/" + goruntime.GOARCH
	switch goruntime.GOOS {
	case "darwin", "linux":
		out = append(out, Check{Name: "host platform", Status: "ok", Detail: plat})
	case "windows":
		out = append(out, Check{Name: "host platform", Status: "warn", Detail: plat + " (experimental, untested)"})
	default:
		out = append(out, Check{Name: "host platform", Status: "warn", Detail: plat + " (unsupported)"})
	}
	if p, err := exec.LookPath("git"); err == nil {
		out = append(out, Check{Name: "git", Status: "ok", Detail: p + " (used only to fetch remote repositories, hooks disabled)"})
	} else {
		out = append(out, Check{Name: "git", Status: "warn", Detail: "not found", Suggestion: "Install git to inspect GitHub URLs; local directories work without it."})
	}
	if err := os.MkdirAll(s.Paths.Home, 0o700); err != nil {
		out = append(out, Check{Name: "data directory", Status: "fail", Detail: err.Error()})
	} else {
		probe := filepath.Join(s.Paths.Home, ".doctor-probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			out = append(out, Check{Name: "data directory", Status: "fail", Detail: s.Paths.Home + " not writable: " + err.Error()})
		} else {
			os.Remove(probe)
			out = append(out, Check{Name: "data directory", Status: "ok", Detail: s.Paths.Home})
		}
	}
	if _, err := s.Store.List(ctx, true, 1); err != nil {
		out = append(out, Check{Name: "session database", Status: "fail", Detail: err.Error()})
	} else {
		out = append(out, Check{Name: "session database", Status: "ok", Detail: s.Paths.DB})
	}
	b, err := s.Backend(ctx)
	if err != nil {
		out = append(out, Check{Name: "container engine", Status: "fail", Detail: "unreachable: " + err.Error(),
			Suggestion: "Start Docker Desktop, OrbStack, or `colima start`. Inspection still works without it."})
		return out
	}
	info, err := b.Check(ctx)
	if err != nil {
		out = append(out, Check{Name: "container engine", Status: "fail", Detail: err.Error()})
		return out
	}
	out = append(out, Check{Name: "container engine", Status: "ok",
		Detail: fmt.Sprintf("Docker %s (API %s) at %s [%s] — %s %s, %d CPUs, %.1f GiB", info.ServerVersion, info.APIVersion, info.Endpoint, info.EndpointFrom,
			info.OS, info.Arch, info.CPUs, float64(info.MemTotalBytes)/(1<<30))})
	lim := Check{Name: "resource limits", Status: "ok", Detail: "memory and PIDs limits enforceable (cgroup " + info.CgroupVersion + ")"}
	if !info.MemoryLimit || !info.PidsLimit {
		lim = Check{Name: "resource limits", Status: "fail", Detail: "the engine cannot enforce memory/PIDs limits; execution will be refused (fail closed)"}
	}
	out = append(out, lim)
	if info.Rootless {
		out = append(out, Check{Name: "rootless", Status: "ok", Detail: "engine runs rootless"})
	}
	out = append(out, Check{Name: "isolation", Status: "warn",
		Detail:     "containers are not a strong security boundary and outbound network is not filtered",
		Suggestion: "Only run repositories you are willing to trust with network access from a container. See docs/THREAT_MODEL.md."})
	return out
}

func (s *Service) backendInfo(ctx context.Context) (*runtime.Info, error) {
	b, err := s.Backend(ctx)
	if err != nil {
		return nil, err
	}
	return b.Check(ctx)
}
