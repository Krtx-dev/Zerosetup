// Package policy enforces ZEROSETUP's execution security policy on a plan.
// It fails closed: anything not explicitly allowed is rejected.
package policy

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/zerosetup/zerosetup/internal/services"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Violation is a single policy failure.
type Violation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

var (
	imageRe = regexp.MustCompile(`^docker\.io/library/(node|python):[0-9][0-9.]*-slim$`)
	binRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// allowedBins may appear as argv[0]. Shells are deliberately absent.
var allowedBins = map[string]bool{"node": true, "npm": true, "pnpm": true, "yarn": true, "python": true}

// deniedEnv cannot be set by plans: they control the loader, our layout,
// or the container runtime.
var deniedEnv = map[string]bool{
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true, "PATH": true, "HOME": true,
	"DOCKER_HOST": true, "DOCKER_CONFIG": true, "NPM_CONFIG_PREFIX": true, "npm_config_prefix": true,
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true, "fish": true, "ksh": true, "csh": true, "env": true, "sudo": true, "su": true}

var upRe = regexp.MustCompile(`^((?:\.\./)*)node_modules/\.bin/([A-Za-z0-9][A-Za-z0-9._-]{0,63})$`)

// checkArgv0 validates the executable. A workspace command may reference
// hoisted binaries with ../ segments, but never above the source root.
func checkArgv0(a, workdir string) error {
	if m := upRe.FindStringSubmatch(a); m != nil && m[1] != "" {
		ups := strings.Count(m[1], "../")
		depth := 0
		if workdir != "" {
			depth = strings.Count(path.Clean(workdir), "/") + 1
		}
		if ups > depth {
			return fmt.Errorf("%q escapes the source directory", a)
		}
		return nil
	}
	base := path.Base(a)
	if shells[base] {
		return fmt.Errorf("%q is a shell or privilege tool; commands must not be interpreted by a shell", a)
	}
	switch {
	case allowedBins[a]:
		return nil
	case strings.HasPrefix(a, "node_modules/.bin/") && binRe.MatchString(strings.TrimPrefix(a, "node_modules/.bin/")):
		return nil
	case strings.HasPrefix(a, ps.VenvDir+"/bin/") && binRe.MatchString(strings.TrimPrefix(a, ps.VenvDir+"/bin/")):
		return nil
	}
	return fmt.Errorf("%q is not an allowed executable", a)
}

// Check returns all violations for p.
func Check(p *ps.Plan) []Violation {
	var v []Violation
	add := func(rule, f string, a ...any) { v = append(v, Violation{Rule: rule, Message: fmt.Sprintf(f, a...)}) }

	if !p.Compatibility.Runnable {
		add("runnable", "plan is not runnable: %s", strings.Join(p.Compatibility.Reasons, "; "))
	}
	if !imageRe.MatchString(p.Image.Reference) {
		add("image-allowlist", "image %q is not an allowlisted official runtime image", p.Image.Reference)
	}
	cmds := []ps.Command{p.Start}
	for _, s := range p.Steps {
		cmds = append(cmds, s.Command)
	}
	for _, c := range cmds {
		if len(c.Argv) == 0 {
			continue
		}
		if err := checkArgv0(c.Argv[0], c.Workdir); err != nil {
			add("no-shell", "%v", err)
		}
	}
	svcImages := services.Images()
	for _, sv := range p.Services {
		if !svcImages[sv.Image] {
			add("service-allowlist", "service image %q is not allowlisted", sv.Image)
		}
		if sv.User == "" || strings.HasPrefix(sv.User, "0:") || sv.User == "0" {
			add("non-root", "service %s must run as a non-root user", sv.Kind)
		}
		if len(sv.ReadyCommand) == 0 || shells[path.Base(sv.ReadyCommand[0])] {
			add("no-shell", "service %s readiness must be a direct command", sv.Kind)
		}
	}
	if p.Network.PublishHost != "127.0.0.1" {
		add("loopback-only", "ports must be published on 127.0.0.1, not %q", p.Network.PublishHost)
	}
	if p.Network.OutboundFiltered {
		add("honest-network", "plan claims outbound filtering, which this backend does not enforce")
	}
	if p.Filesystem.HostMounts {
		add("no-host-mounts", "host mounts are not allowed")
	}
	if !p.Filesystem.ReadonlyRootfs {
		add("readonly-rootfs", "container root filesystem must be read-only")
	}
	r := p.Resources
	if r.MemoryMB <= 0 || r.MemoryMB > 16384 {
		add("limits", "memory limit must be 1-16384 MB")
	}
	if r.CPUs <= 0 || r.CPUs > 16 {
		add("limits", "cpu limit must be >0 and <=16")
	}
	if r.PidsLimit <= 0 || r.PidsLimit > 8192 {
		add("limits", "pids limit must be 1-8192")
	}
	if r.LogMaxSizeMB <= 0 || r.LogMaxSizeMB > 100 {
		add("limits", "log size must be 1-100 MB")
	}
	for _, e := range p.Env {
		if deniedEnv[e.Name] {
			add("env-denylist", "environment variable %s cannot be set by a plan", e.Name)
		}
		if e.Secret && e.Value != "" {
			add("no-secrets-in-plan", "secret %s carries a value", e.Name)
		}
	}
	for _, pt := range p.Ports {
		if pt.ContainerPort < 1 || pt.ContainerPort > 65535 {
			add("ports", "invalid port %d", pt.ContainerPort)
		}
	}
	return v
}

// Enforce returns a structured error if p violates policy.
func Enforce(p *ps.Plan) error {
	v := Check(p)
	if len(v) == 0 {
		return nil
	}
	msgs := make([]string, len(v))
	for i, x := range v {
		msgs[i] = "[" + x.Rule + "] " + x.Message
	}
	return zerr.New(zerr.CodePolicyViolation, "policy", "plan rejected by security policy").
		WithDetail("%s", strings.Join(msgs, "\n"))
}
