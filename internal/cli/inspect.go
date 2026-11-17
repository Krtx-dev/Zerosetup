package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

type planFlags struct {
	ref          string
	port         int
	runtimeVer   string
	script       string
	readyPath    string
	readyTimeout int
	env          []string
	secrets      []string
	secretsEnv   []string
	memory       int64
	cpus         float64
	mode         string
	workspace    string
	services     []string
}

func (f *planFlags) register(cmd *cobra.Command, withSecrets bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.ref, "ref", "", "branch, tag or full commit SHA to inspect")
	fl.IntVar(&f.port, "port", 0, "override the application's container port")
	fl.StringVar(&f.runtimeVer, "runtime-version", "", "override the runtime version (e.g. 22 or 3.12)")
	fl.StringVar(&f.script, "script", "", "package.json script to run instead of the detected one")
	fl.StringVar(&f.readyPath, "ready-path", "", "HTTP path used for the readiness check")
	fl.IntVar(&f.readyTimeout, "ready-timeout", 0, "readiness timeout in seconds")
	fl.StringArrayVar(&f.env, "env", nil, "non-secret environment variable KEY=VALUE (repeatable)")
	fl.Int64Var(&f.memory, "memory", 0, "memory limit in MB")
	fl.Float64Var(&f.cpus, "cpus", 0, "CPU limit")
	fl.StringVar(&f.mode, "mode", "", "dev (default) or production (build, then serve the build)")
	fl.StringVar(&f.workspace, "workspace", "", "npm workspace directory to run in a monorepo (e.g. apps/web)")
	fl.StringArrayVar(&f.services, "with-service", nil, "start a managed service for the app: postgres or redis (repeatable)")
	if withSecrets {
		fl.StringArrayVar(&f.secrets, "secret", nil, "secret environment variable NAME; you are prompted for its value (repeatable)")
		fl.StringArrayVar(&f.secretsEnv, "secret-from-env", nil, "secret NAME whose value is read from your current environment (repeatable)")
	}
}

func (f *planFlags) overrides() (planner.Overrides, error) {
	ov := planner.Overrides{Port: f.port, RuntimeVersion: f.runtimeVer, Script: f.script, ReadinessPath: f.readyPath,
		ReadyTimeoutSec: f.readyTimeout, MemoryMB: f.memory, CPUs: f.cpus, Mode: f.mode, Workspace: f.workspace, Services: f.services}
	for _, kv := range f.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return ov, zerr.New(zerr.CodePlanInvalid, "plan", "--env must be KEY=VALUE, got "+kv)
		}
		if ov.Env == nil {
			ov.Env = map[string]string{}
		}
		ov.Env[k] = v
	}
	ov.SecretNames = append(append(ov.SecretNames, f.secrets...), f.secretsEnv...)
	return ov, ov.Validate()
}

func (a *app) cmdInspect() *cobra.Command {
	var f planFlags
	cmd := &cobra.Command{
		Use:   "inspect <github-url | directory>",
		Short: "Analyse a repository without executing any of its code",
		Example: `  zerosetup inspect https://github.com/owner/repo
  zerosetup inspect ./my-project --json
  zerosetup inspect https://github.com/owner/repo --ref v1.2.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.inspect(cmd, args[0], &f)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.emitJSON(res)
			}
			a.ui.Header("inspect")
			a.printInspection(res, false)
			return nil
		},
	}
	f.register(cmd, false)
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}

func (a *app) cmdPlan() *cobra.Command {
	var f planFlags
	var raw bool
	cmd := &cobra.Command{
		Use:   "plan <github-url | directory>",
		Short: "Generate and explain the execution plan (no code is executed)",
		Example: `  zerosetup plan https://github.com/owner/repo
  zerosetup plan . --port 8080 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.inspect(cmd, args[0], &f)
			if err != nil {
				return err
			}
			if raw {
				b, err := ps.Canonical(res.Plan)
				if err != nil {
					return err
				}
				fmt.Fprintln(a.out, string(b))
				return nil
			}
			if a.jsonOut {
				return a.emitJSON(map[string]any{"planHash": res.PlanHash, "plan": res.Plan, "policyViolations": res.Violations})
			}
			a.ui.Header("plan")
			a.printInspection(res, true)
			return nil
		},
	}
	f.register(cmd, false)
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&raw, "raw", false, "print only the canonical plan JSON (the exact bytes that are hashed)")
	return cmd
}

func (a *app) inspect(cmd *cobra.Command, src string, f *planFlags) (*engine.InspectResult, error) {
	ov, err := f.overrides()
	if err != nil {
		return nil, err
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Inspect(cmd.Context(), engine.InspectRequest{Source: src, Ref: f.ref, Overrides: ov})
}

func decision(p *ps.Plan, field string) *ps.Decision {
	for i := range p.Decisions {
		if p.Decisions[i].Field == field {
			return &p.Decisions[i]
		}
	}
	return nil
}

func evidenceText(d *ps.Decision) string {
	if d == nil {
		return ""
	}
	return d.Reason
}

func (a *app) printInspection(res *engine.InspectResult, full bool) {
	u, p := a.ui, res.Plan
	src := p.Source.Repository
	if src == "" {
		src = tildePath(res.Resolved.Dir)
	}
	if p.Source.Subdir != "" {
		src += "/" + p.Source.Subdir
	}
	if p.Source.Commit != "" {
		src += " @ " + p.Source.Commit[:12]
	}
	if res.Resolved.CacheHit {
		src += u.Dim("  (source cache hit)")
	}
	u.Line("ok", "Repository resolved  "+u.Code(src))
	if p.Source.Commit == "" && p.Source.Kind == "local" {
		u.Line("warn", "Local directory is not a git checkout (or HEAD is unreadable); identified by content hash "+u.Dim(p.Source.ContentHash[:19]))
	}
	u.Line("ok", fmt.Sprintf("Inspected without executing code  %s", u.Dim(fmt.Sprintf("(%d files, %d ms)", res.Inspection.Report.Files, res.Timing["inspect"]))))
	u.Section("Detected")
	row := func(label, field, val string) {
		d := decision(p, field)
		kind := ""
		if d != nil {
			kind = string(d.Kind)
			if val == "" {
				val = d.Value
			}
		}
		if val == "" {
			val = "—"
		}
		u.KV(label, val, kind, evidenceText(d))
	}
	row("Language", "language", p.Stack.Language)
	row("Framework", "framework", p.Stack.Framework)
	rt := p.Stack.Runtime
	if p.Stack.RuntimeVersion != "" {
		rt += " " + p.Stack.RuntimeVersion
	}
	row("Runtime", "runtimeVersion", rt)
	pm := p.Stack.PackageManager
	if p.Stack.PackageManagerVersion != "" && p.Stack.PackageManagerVersion != "bundled" {
		pm += " " + p.Stack.PackageManagerVersion
	}
	row("Packages", "packageManager", pm)
	if len(p.Start.Argv) > 0 {
		row("Start", "start", strings.Join(p.Start.Argv, " "))
	}
	if p.PrimaryPort() > 0 {
		row("Port", "port", fmt.Sprint(p.PrimaryPort()))
	}
	if len(res.Services) > 0 {
		var s []string
		for _, sv := range res.Services {
			s = append(s, fmt.Sprintf("%s (%s: %s)", sv.Kind, sv.File, sv.Evidence))
		}
		u.KV("Services", strings.Join(s, ", "), "UNSUPPORTED", "likely needed; not provisioned automatically in this version")
	}
	if full {
		u.Section("Execution plan")
		u.KV("Image", p.Image.Reference, "", "")
		for i, st := range p.Steps {
			net := ""
			if st.NeedsNetwork {
				net = u.Dim("  [network]")
			}
			u.Printf("  %s %s%s\n", u.Dim(fmt.Sprintf("%-14s", fmt.Sprintf("%d. %s", i+1, st.Stage))), u.Code(strings.Join(st.Command.Argv, " ")), net)
			if st.Reason != "" {
				u.Printf("  %-14s %s\n", "", u.Dim(st.Reason))
			}
		}
		if len(p.Start.Argv) > 0 {
			u.Printf("  %s %s\n", u.Dim(fmt.Sprintf("%-14s", fmt.Sprintf("%d. start", len(p.Steps)+1))), u.Code(strings.Join(p.Start.Argv, " ")))
		}
		for _, sv := range p.Services {
			u.KV("Service", fmt.Sprintf("%s (%s) as %q on the private session network, uid %s, not published", sv.Kind, sv.Image, sv.Alias, sv.User), "USER_OVERRIDE", "ready when: "+strings.Join(sv.ReadyCommand, " "))
		}
		for _, r := range p.Readiness {
			u.KV("Readiness", fmt.Sprintf("listening socket on :%d, then HTTP GET %s (timeout %ds)", r.Port, r.Path, r.TimeoutSec), "", "")
		}
		u.KV("Limits", fmt.Sprintf("%.2g CPU · %d MB · %d PIDs · logs %d MB", p.Resources.CPUs, p.Resources.MemoryMB, p.Resources.PidsLimit, p.Resources.LogMaxSizeMB), "", "")
		u.KV("Isolation", "non-root uid 1000 · all capabilities dropped · no-new-privileges · read-only rootfs · no host mounts", "", "")
		u.KV("Network", "preview on 127.0.0.1 only · outbound internet NOT filtered (install and runtime)", "", "")
		if len(p.Env) > 0 {
			u.Section("Environment")
			for _, e := range p.Env {
				v := e.Value
				if e.Secret {
					v = "(secret — supplied at run time, never stored)"
				}
				u.KV(e.Name, v, string(e.Kind), e.Reason)
			}
		}
	}
	if len(p.Warnings) > 0 {
		u.Section("Warnings")
		for _, w := range p.Warnings {
			u.Line("warn", w.Message)
		}
	}
	if len(p.Conflicts) > 0 {
		u.Section("Conflicting signals")
		for _, c := range p.Conflicts {
			u.Line("warn", fmt.Sprintf("%s: %s", c.Field, strings.Join(c.Signals, " vs ")))
			u.Printf("      %s\n", u.Dim(c.Resolution))
		}
	}
	if len(p.ExistingConfigs) > 0 {
		u.Section("Existing environment specs (analysed, not executed)")
		for _, ec := range p.ExistingConfigs {
			u.Line("info", fmt.Sprintf("%s %s", u.Code(ec.File), u.Dim("("+ec.Kind+")")))
			for _, fd := range ec.Findings {
				kind := "info"
				if strings.HasPrefix(fd, "UNSAFE") {
					kind = "warn"
				}
				u.Printf("      %s %s\n", u.sym(kind), fd)
			}
		}
	}
	u.Section("Compatibility")
	switch {
	case !p.Compatibility.Runnable:
		u.Line("bad", "Not runnable by ZEROSETUP:")
		for _, r := range p.Compatibility.Reasons {
			u.Printf("      %s\n", r)
		}
	case p.Compatibility.Level == "experimental":
		u.Line("warn", "Runnable (experimental): "+strings.Join(p.Compatibility.Reasons, "; "))
	default:
		u.Line("ok", "Supported")
	}
	for _, v := range res.Violations {
		if v.Rule != "runnable" {
			u.Line("bad", "policy: "+v.Message)
		}
	}
	u.Printf("\n  %s %s\n\n", u.Dim("plan"), u.Accent(ps.ShortHash(res.PlanHash)))
}

// tildePath abbreviates the user's home directory so output (and demo
// transcripts) do not carry personal paths.
func tildePath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(p, h+"/") {
		return "~" + p[len(h):]
	}
	return p
}
