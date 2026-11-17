// Package planner builds a versioned execution plan from an inspection.
//
// Precedence (highest first): explicit ZEROSETUP overrides, validated
// existing environment specs (none are used yet), runtime and lockfile
// declarations, framework detection, documented defaults. Every choice is
// recorded as a Decision with its Kind and evidence.
package planner

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zerosetup/zerosetup/internal/detect"
	"github.com/zerosetup/zerosetup/internal/inspector"
	"github.com/zerosetup/zerosetup/internal/services"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Default resource limits (enforced by the backend).
const (
	DefaultCPUs            = 2.0
	DefaultMemoryMB        = 2048
	DefaultPidsLimit       = 1024
	DefaultLogMaxSizeMB    = 10
	DefaultStartTimeoutSec = 180
)

// Overrides is the closed set of user overrides. There is deliberately no
// way to override commands, images or mounts.
type Overrides struct {
	Port            int               `json:"port,omitempty"`
	RuntimeVersion  string            `json:"runtimeVersion,omitempty"`
	Script          string            `json:"script,omitempty"`
	ReadinessPath   string            `json:"readinessPath,omitempty"`
	ReadyTimeoutSec int               `json:"readyTimeoutSec,omitempty"`
	Env             map[string]string `json:"env,omitempty"`         // non-secret values
	SecretNames     []string          `json:"secretNames,omitempty"` // values supplied at run time only
	MemoryMB        int64             `json:"memoryMB,omitempty"`
	CPUs            float64           `json:"cpus,omitempty"`
	Mode            string            `json:"mode,omitempty"`      // dev (default) | production
	Workspace       string            `json:"workspace,omitempty"` // npm workspace directory
	Services        []string          `json:"services,omitempty"`  // managed services: postgres, redis
}

var (
	scriptRe    = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,64}$`)
	envNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	pathRe      = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/%-]{0,255}$`)
	versionRe   = regexp.MustCompile(`^v?\d{1,2}(\.\d{1,3}){0,2}$`)
	workspaceRe = regexp.MustCompile(`^[A-Za-z0-9._@-]+(/[A-Za-z0-9._@-]+){0,4}$`)
)

// Validate checks overrides before they reach detectors.
func (o Overrides) Validate() error {
	var probs []string
	if o.Port != 0 && (o.Port < 1 || o.Port > 65535) {
		probs = append(probs, "port must be 1-65535")
	}
	if o.RuntimeVersion != "" && !versionRe.MatchString(o.RuntimeVersion) {
		probs = append(probs, "runtime version must look like 22 or 3.12")
	}
	if o.Script != "" && !scriptRe.MatchString(o.Script) {
		probs = append(probs, "script name contains unsupported characters")
	}
	if o.ReadinessPath != "" && !pathRe.MatchString(o.ReadinessPath) {
		probs = append(probs, "readiness path must start with / and contain only URL path characters")
	}
	if o.ReadyTimeoutSec < 0 || o.ReadyTimeoutSec > 1800 {
		probs = append(probs, "readiness timeout must be 1-1800 seconds")
	}
	for k, v := range o.Env {
		if !envNameRe.MatchString(k) {
			probs = append(probs, fmt.Sprintf("invalid env name %q", k))
		}
		if strings.ContainsRune(v, 0) {
			probs = append(probs, fmt.Sprintf("env %s contains NUL", k))
		}
	}
	for _, k := range o.SecretNames {
		if !envNameRe.MatchString(k) {
			probs = append(probs, fmt.Sprintf("invalid secret name %q", k))
		}
	}
	if o.MemoryMB != 0 && (o.MemoryMB < 256 || o.MemoryMB > 16384) {
		probs = append(probs, "memory must be 256-16384 MB")
	}
	if o.CPUs != 0 && (o.CPUs < 0.25 || o.CPUs > 16) {
		probs = append(probs, "cpus must be 0.25-16")
	}
	if o.Mode != "" && o.Mode != "dev" && o.Mode != "production" {
		probs = append(probs, "mode must be dev or production")
	}
	if o.Workspace != "" {
		c := path.Clean(o.Workspace)
		if !workspaceRe.MatchString(o.Workspace) || c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
			probs = append(probs, "workspace must be a relative directory inside the repository")
		}
	}
	seen := map[string]bool{}
	for _, sv := range o.Services {
		if sv != "postgres" && sv != "redis" {
			probs = append(probs, fmt.Sprintf("unsupported service %q (supported: postgres, redis)", sv))
		}
		if seen[sv] {
			probs = append(probs, "duplicate service "+sv)
		}
		seen[sv] = true
	}
	if len(probs) > 0 {
		return zerr.New(zerr.CodePlanInvalid, "plan", "invalid override: "+strings.Join(probs, "; "))
	}
	return nil
}

// Hints converts overrides to detector hints.
func (o Overrides) Hints() detect.Hints {
	return detect.Hints{Script: o.Script, RuntimeVersion: o.RuntimeVersion, Port: o.Port, Mode: o.Mode, Workspace: o.Workspace}
}

// Build creates a plan from an inspection and overrides.
func Build(in *inspector.Inspection, ov Overrides) (*ps.Plan, error) {
	if err := ov.Validate(); err != nil {
		return nil, err
	}
	p := &ps.Plan{
		SchemaVersion: ps.SchemaVersion,
		Source:        in.Source,
		Resources: ps.Resources{CPUs: DefaultCPUs, MemoryMB: DefaultMemoryMB, PidsLimit: DefaultPidsLimit,
			LogMaxSizeMB: DefaultLogMaxSizeMB, StartTimeoutSec: DefaultStartTimeoutSec},
		Network:    ps.NetworkPolicy{PublishHost: "127.0.0.1", InstallOutbound: true, RuntimeOutbound: true, OutboundFiltered: false},
		Filesystem: ps.FilesystemPolicy{HostMounts: false, ReadonlyRootfs: true},
	}
	rep := in.Report
	p.ExistingConfigs = rep.Existing
	if rep.Truncated {
		p.Warnings = append(p.Warnings, ps.Warning{Code: "INSPECTION_TRUNCATED", Message: "The repository has more files than the inspection limit; detection only considered the first entries."})
	}
	pr := rep.Primary
	if pr == nil {
		p.Compatibility = ps.Compatibility{Runnable: false, Level: "unsupported",
			Reasons: []string{"no supported language detected (supported: Node.js via package.json, Python via pyproject.toml/requirements.txt)"}}
		for _, ec := range rep.Existing {
			p.Compatibility.Reasons = append(p.Compatibility.Reasons, fmt.Sprintf("found %s (%s), which ZEROSETUP does not execute", ec.File, ec.Kind))
		}
		return ps.Normalize(p), nil
	}
	p.Stack = pr.Stack
	p.Image = ps.Image{Reference: pr.Image}
	p.Steps = pr.Steps
	p.Start = pr.Start
	p.Decisions = append(p.Decisions, pr.Decisions...)
	allRequested := true
	for _, sv := range pr.Services {
		found := false
		for _, k := range ov.Services {
			found = found || k == sv.Kind
		}
		allRequested = allRequested && found
	}
	for _, w := range pr.Warnings {
		if w.Code == "SERVICES_NOT_PROVISIONED" && allRequested {
			continue
		}
		p.Warnings = append(p.Warnings, w)
	}
	p.Conflicts = append(p.Conflicts, pr.Conflicts...)
	for _, other := range rep.Results[1:] {
		p.Warnings = append(p.Warnings, ps.Warning{Code: "SECONDARY_STACK",
			Message: fmt.Sprintf("Also found a %s project (%s); only the %s application is run.", other.Stack.Runtime, other.Stack.Framework, pr.Stack.Runtime)})
	}
	for _, s := range pr.Services {
		requested := false
		for _, k := range ov.Services {
			requested = requested || k == s.Kind
		}
		if !requested {
			p.Decisions = append(p.Decisions, ps.Decision{Field: "service", Value: s.Kind, Kind: ps.KindInferred, Certainty: ps.CertaintyMedium,
				Reason: s.Kind + " may be required; not started unless you pass --with-service " + s.Kind, Evidence: []ps.Evidence{s.Evidence}})
		}
	}
	for _, ec := range rep.Existing {
		if ec.Kind == "dockerfile" {
			for _, f := range ec.Findings {
				if exp, ok := strings.CutPrefix(f, "exposes: "); ok && pr.Port > 0 && !strings.Contains(exp, strconv.Itoa(pr.Port)) {
					p.Conflicts = append(p.Conflicts, ps.Conflict{Field: "port",
						Signals:    []string{fmt.Sprintf("plan=%d", pr.Port), ec.File + " EXPOSE " + exp},
						Resolution: "the dev-server port is used; the Dockerfile describes a production image. Override with --port if needed."})
				}
			}
		}
	}

	if pr.Port > 0 {
		p.Ports = []ps.Port{{Name: "http", ContainerPort: pr.Port, Protocol: "http"}}
	}
	// Environment: detector values, then managed services, then overrides.
	env := map[string]ps.EnvVar{}
	for _, e := range pr.Env {
		env[e.Name] = e
	}
	svcs := append([]string{}, ov.Services...)
	sort.Strings(svcs)
	for _, k := range svcs {
		def, err := services.Lookup(k)
		if err != nil {
			return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "plan", "invalid service")
		}
		p.Services = append(p.Services, def.Service)
		for _, e := range def.AppEnv {
			env[e.Name] = e
		}
		p.Overrides = append(p.Overrides, "service:"+k)
		p.Decisions = append(p.Decisions, ps.Decision{Field: "service", Value: k, Kind: ps.KindOverride, Certainty: ps.CertaintyDeclared,
			Reason: def.Service.Reason + "; reachable only from the app as host " + def.Service.Alias})
	}
	for k, v := range ov.Env {
		env[k] = ps.EnvVar{Name: k, Value: v, Kind: ps.KindOverride, Reason: "set by --env"}
		p.Overrides = append(p.Overrides, "env:"+k)
	}
	for _, k := range ov.SecretNames {
		env[k] = ps.EnvVar{Name: k, Secret: true, Required: true, Kind: ps.KindOverride, Reason: "secret supplied at run time; value never stored"}
		p.Overrides = append(p.Overrides, "secret:"+k)
	}
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		p.Env = append(p.Env, env[k])
	}

	if pr.Port > 0 {
		rc := ps.ReadinessCheck{Type: "http", Port: pr.Port, Path: pr.Readiness.Path, TimeoutSec: DefaultStartTimeoutSec}
		if rc.Path == "" {
			rc.Path = "/"
		}
		if ov.ReadinessPath != "" {
			rc.Path = ov.ReadinessPath
			p.Overrides = append(p.Overrides, "readinessPath")
			p.Decisions = append(p.Decisions, ps.Decision{Field: "readiness", Value: "GET " + rc.Path, Kind: ps.KindOverride, Certainty: ps.CertaintyDeclared, Reason: "set by --ready-path"})
		}
		if ov.ReadyTimeoutSec > 0 {
			rc.TimeoutSec = ov.ReadyTimeoutSec
			p.Resources.StartTimeoutSec = ov.ReadyTimeoutSec
			p.Overrides = append(p.Overrides, "readyTimeout")
		}
		p.Readiness = []ps.ReadinessCheck{rc}
	}
	if ov.Port > 0 {
		p.Overrides = append(p.Overrides, "port")
	}
	if ov.RuntimeVersion != "" {
		p.Overrides = append(p.Overrides, "runtimeVersion")
	}
	if ov.Script != "" {
		p.Overrides = append(p.Overrides, "script")
	}
	if ov.Mode == "production" {
		p.Overrides = append(p.Overrides, "mode")
	}
	if ov.Workspace != "" {
		p.Overrides = append(p.Overrides, "workspace")
	}
	if ov.MemoryMB > 0 {
		p.Resources.MemoryMB = ov.MemoryMB
		p.Overrides = append(p.Overrides, "memoryMB")
	}
	if ov.CPUs > 0 {
		p.Resources.CPUs = ov.CPUs
		p.Overrides = append(p.Overrides, "cpus")
	}
	sort.Strings(p.Overrides)

	switch {
	case !pr.Runnable():
		p.Compatibility = ps.Compatibility{Runnable: false, Level: "unsupported", Reasons: pr.Unsupported}
	case pr.Experimental:
		p.Compatibility = ps.Compatibility{Runnable: true, Level: "experimental", Reasons: []string{"uses a package manager or layout whose support is experimental"}}
	default:
		p.Compatibility = ps.Compatibility{Runnable: true, Level: "supported"}
	}
	p = ps.Normalize(p)
	if err := ps.Validate(p); err != nil {
		return nil, zerr.Wrap(err, zerr.CodePlanInvalid, "plan", "generated plan failed validation (this is a ZEROSETUP bug)")
	}
	return p, nil
}
