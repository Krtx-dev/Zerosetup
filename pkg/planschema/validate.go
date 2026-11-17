package planschema

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidationError lists every structural problem found in a plan.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid plan: " + strings.Join(e.Problems, "; ")
}

// Validate checks structural validity (types, ranges, required fields). It
// does NOT apply security policy; that is internal/policy's job.
func Validate(p *Plan) error {
	var probs []string
	add := func(f string, a ...any) { probs = append(probs, fmt.Sprintf(f, a...)) }

	if !SupportedVersions[p.SchemaVersion] {
		add("unsupported schemaVersion %q", p.SchemaVersion)
	}
	aliases := map[string]bool{}
	for i, sv := range p.Services {
		if sv.Kind == "" || sv.Image == "" || sv.Alias == "" || sv.Port < 1 || sv.Port > 65535 || len(sv.ReadyCommand) == 0 || sv.User == "" {
			add("services[%d] is incomplete", i)
		}
		if aliases[sv.Alias] {
			add("services[%d].alias %q duplicated", i, sv.Alias)
		}
		aliases[sv.Alias] = true
	}
	if p.Source.Kind != "github" && p.Source.Kind != "local" {
		add("source.kind must be github or local")
	}
	if p.Source.ContentHash == "" {
		add("source.contentHash is required")
	}
	if p.Compatibility.Runnable {
		if p.Image.Reference == "" {
			add("image.reference is required")
		}
		if len(p.Start.Argv) == 0 {
			add("start.argv is required")
		}
		if len(p.Ports) == 0 {
			add("at least one port is required")
		}
		if len(p.Readiness) == 0 {
			add("at least one readiness check is required")
		}
	}
	checkCmd := func(where string, c Command) {
		for i, a := range c.Argv {
			if a == "" {
				add("%s.argv[%d] is empty", where, i)
			}
			if strings.ContainsRune(a, 0) {
				add("%s.argv[%d] contains NUL", where, i)
			}
		}
		if c.Workdir != "" && !isSafeRel(c.Workdir) {
			add("%s.workdir %q must be a relative path inside the source", where, c.Workdir)
		}
	}
	ids := map[string]bool{}
	for i, s := range p.Steps {
		w := fmt.Sprintf("steps[%d]", i)
		if s.ID == "" || ids[s.ID] {
			add("%s.id must be unique and non-empty", w)
		}
		ids[s.ID] = true
		if s.Stage != StageInstall && s.Stage != StageBuild {
			add("%s.stage must be install or build", w)
		}
		if len(s.Command.Argv) == 0 {
			add("%s.command.argv is required", w)
		}
		if s.TimeoutSec <= 0 || s.TimeoutSec > 3600 {
			add("%s.timeoutSec must be in 1..3600", w)
		}
		checkCmd(w+".command", s.Command)
	}
	checkCmd("start", p.Start)
	ports := map[int]bool{}
	for i, pt := range p.Ports {
		if pt.ContainerPort < 1 || pt.ContainerPort > 65535 {
			add("ports[%d].containerPort out of range", i)
		}
		if pt.Protocol != "http" {
			add("ports[%d].protocol must be http", i)
		}
		ports[pt.ContainerPort] = true
	}
	for i, r := range p.Readiness {
		if r.Type != "http" {
			add("readiness[%d].type must be http", i)
		}
		if !ports[r.Port] {
			add("readiness[%d].port %d is not a declared port", i, r.Port)
		}
		if r.Path != "" && !strings.HasPrefix(r.Path, "/") {
			add("readiness[%d].path must start with /", i)
		}
		if r.TimeoutSec <= 0 || r.TimeoutSec > 1800 {
			add("readiness[%d].timeoutSec must be in 1..1800", i)
		}
		for _, s := range r.ExpectStatus {
			if s < 100 || s > 599 {
				add("readiness[%d].expectStatus %d invalid", i, s)
			}
		}
	}
	seen := map[string]bool{}
	for i, e := range p.Env {
		if !envNameRe.MatchString(e.Name) {
			add("env[%d].name %q is invalid", i, e.Name)
		}
		if seen[e.Name] {
			add("env[%d].name %q duplicated", i, e.Name)
		}
		seen[e.Name] = true
		if e.Secret && e.Value != "" {
			add("env[%d] %s is secret and must not carry a value in the plan", i, e.Name)
		}
	}
	r := p.Resources
	if p.Compatibility.Runnable {
		if r.CPUs <= 0 || r.MemoryMB < 128 || r.PidsLimit <= 0 || r.LogMaxSizeMB <= 0 || r.StartTimeoutSec <= 0 {
			add("resources must all be positive (memoryMB >= 128)")
		}
	}
	if p.Filesystem.HostMounts {
		add("filesystem.hostMounts must be false")
	}
	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}

func isSafeRel(p string) bool {
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	c := path.Clean(p)
	return c != ".." && !strings.HasPrefix(c, "../")
}
