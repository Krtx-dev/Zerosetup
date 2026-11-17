package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func (a *app) cmdRun() *cobra.Command {
	var f planFlags
	var approve, sessionID string
	var open, quiet bool
	cmd := &cobra.Command{
		Use:   "run [github-url | directory]",
		Short: "Inspect, approve and run a repository in an isolated container",
		Long: `run inspects the repository, shows the execution plan, and asks for your
approval before any repository code (including dependency install scripts)
executes. Approval is bound to the plan's hash: if the plan changes, you are
asked again.

In non-interactive contexts pass --approve <plan-hash> after reviewing the
plan with ` + "`zerosetup plan`" + `. There is no flag that approves any plan blindly.`,
		Example: `  zerosetup run https://github.com/owner/repo
  zerosetup plan ./app && zerosetup run ./app --approve 3f9a1c2b7d4e
  zerosetup run --session zs1a2b3c4d          # re-run a failed or repaired session`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, err := a.service()
			if err != nil {
				return err
			}
			var x *sessions.Session
			var plan *ps.Plan
			if sessionID != "" {
				if len(args) > 0 {
					return withExit(ExitUsage, errors.New("pass either a source or --session, not both"))
				}
				if x, err = svc.Status(ctx, sessionID); err != nil {
					return err
				}
				if plan, err = svc.Plan(ctx, x); err != nil {
					return err
				}
				a.ui.Header("run " + x.ID)
				a.ui.Line("info", fmt.Sprintf("session %s (%s) · plan %s", x.ID, x.State, a.ui.Accent(ps.ShortHash(x.PlanHash))))
			} else {
				if len(args) != 1 {
					return withExit(ExitUsage, errors.New("run requires a GitHub URL or directory (or --session)"))
				}
				ov, err := f.overrides()
				if err != nil {
					return err
				}
				req := engine.InspectRequest{Source: args[0], Ref: f.ref, Overrides: ov}
				a.ui.Header("run")
				res, err := svc.Inspect(ctx, req)
				if err != nil {
					return err
				}
				if !quiet {
					a.printInspection(res, true)
				}
				if !res.Plan.Compatibility.Runnable {
					return withExit(ExitUnsupported, zerr.New(zerr.CodeUnsupportedStack, "inspect", "this repository is not supported by ZEROSETUP yet").
						WithDetail("%s", strings.Join(res.Plan.Compatibility.Reasons, "\n")))
				}
				if x, err = svc.CreateSession(ctx, req, res); err != nil {
					return err
				}
				plan = res.Plan
			}
			if x.ApprovedAt == nil || x.State == sessions.AwaitingApproval {
				if err := a.approve(ctx, svc, x, plan, approve); err != nil {
					return err
				}
			}
			secrets, err := a.collectSecrets(plan, f.secrets, f.secretsEnv)
			if err != nil {
				return err
			}
			unsub := svc.Subscribe(a.progressPrinter(x.ID))
			x, err = svc.Execute(ctx, x.ID, secrets)
			unsub()
			if err != nil {
				a.printFailure(ctx, svc, x)
				if x != nil && x.State == sessions.Cancelled {
					return withExit(ExitCancelled, err)
				}
				return err
			}
			a.printReady(x)
			if open {
				openBrowser(x.URL)
			}
			return nil
		},
	}
	f.register(cmd, true)
	cmd.Flags().StringVar(&approve, "approve", "", "approve this exact plan hash without prompting (from `zerosetup plan`)")
	cmd.Flags().StringVar(&sessionID, "session", "", "run an existing session (e.g. after a repair or failure)")
	cmd.Flags().BoolVar(&open, "open", false, "open the preview in your browser when ready")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "do not print the full plan before approval")
	return cmd
}

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func (a *app) approve(ctx context.Context, svc *engine.Service, x *sessions.Session, plan *ps.Plan, given string) error {
	short := ps.ShortHash(x.PlanHash)
	if given != "" {
		_, err := svc.Approve(ctx, x.ID, given)
		return err
	}
	stdin, ok := a.in.(*os.File)
	if !ok || !isTTY(stdin) {
		return zerr.New(zerr.CodeApprovalRequired, "approval", "execution requires approval, and no interactive terminal is available").
			WithDetail("Session %s was created with plan %s. Nothing has been executed.", x.ID, short).
			WithSuggestion(fmt.Sprintf("Review the plan, then run: zerosetup run --session %s --approve %s", x.ID, short))
	}
	u := a.ui
	u.Section("Approval required")
	u.Printf("  %s\n", u.Warn("This will execute code from the repository and its dependencies (including install scripts)."))
	u.Printf("  %s\n", u.Dim("Containers limit filesystem access, but they are not a strong security boundary, and outbound"))
	u.Printf("  %s\n", u.Dim("network access is not filtered. Only continue if you are willing to run this repository's code."))
	u.Printf("\n  Run plan %s for %s? [y/N] ", u.Accent(short), u.Code(tildePath(x.Repository)))
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans != "y" && ans != "yes" {
		return withExit(ExitApproval, zerr.New(zerr.CodeApprovalRequired, "approval", "not approved; nothing was executed").
			WithSuggestion(fmt.Sprintf("Approve later with: zerosetup run --session %s --approve %s", x.ID, short)))
	}
	_, err := svc.Approve(ctx, x.ID, x.PlanHash)
	return err
}

func (a *app) collectSecrets(plan *ps.Plan, prompted, fromEnv []string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range fromEnv {
		v, ok := os.LookupEnv(name)
		if !ok {
			return nil, zerr.New(zerr.CodeApprovalRequired, "secrets", "--secret-from-env "+name+": variable is not set in your environment")
		}
		out[name] = v
	}
	needed := map[string]bool{}
	for _, n := range prompted {
		needed[n] = true
	}
	for _, e := range plan.Env {
		if e.Secret && e.Required && out[e.Name] == "" {
			needed[e.Name] = true
		}
	}
	for name := range needed {
		if _, done := out[name]; done {
			continue
		}
		stdin, ok := a.in.(*os.File)
		if !ok || !isTTY(stdin) {
			return nil, zerr.New(zerr.CodeApprovalRequired, "secrets", "secret "+name+" must be entered interactively, or passed with --secret-from-env "+name)
		}
		a.ui.Printf("  Value for secret %s (input hidden): ", a.ui.Code(name))
		b, err := term.ReadPassword(int(stdin.Fd()))
		a.ui.Println()
		if err != nil {
			return nil, err
		}
		out[name] = string(b)
	}
	return out, nil
}

var stageLabel = map[string]string{
	"PREPARING": "Preparing environment", "BUILDING": "Installing dependencies", "STARTING": "Starting application",
	"VERIFYING": "Verifying readiness", "READY": "Ready",
}

func (a *app) progressPrinter(sid string) func(sessions.Event) {
	u := a.ui
	start := time.Now()
	lastLog := ""
	return func(e sessions.Event) {
		if e.SessionID != sid {
			return
		}
		el := u.Dim(fmt.Sprintf("%6.1fs", time.Since(start).Seconds()))
		switch e.Type {
		case "state":
			if l, ok := stageLabel[e.Stage]; ok && e.Stage != "READY" {
				u.Printf("\n  %s %s\n", u.Accent("›"), u.bold.Render(l))
			}
		case "milestone":
			u.Printf("  %s %s %s\n", el, u.sym("ok"), e.Message)
		case "stage":
			u.Printf("  %s %s %s\n", el, u.sym("info"), e.Message)
		case "progress":
			u.Printf("  %s %s %s\n", el, u.Dim("…"), u.Dim(e.Message))
		case "warning":
			u.Printf("  %s %s %s\n", el, u.sym("warn"), e.Message)
		case "diagnostic":
			u.Printf("  %s %s %s\n", el, u.sym("bad"), e.Message)
		case "log":
			lines, _ := e.Data["lines"].([]string)
			if lines == nil {
				if anyLines, ok := e.Data["lines"].([]any); ok {
					for _, l := range anyLines {
						if s, ok := l.(string); ok {
							lines = append(lines, s)
						}
					}
				}
			}
			for _, l := range lines {
				l = strings.TrimRight(l, "\r")
				if strings.TrimSpace(l) == "" || l == lastLog {
					continue
				}
				lastLog = l
				u.Printf("  %s   %s\n", u.Dim("      │"), u.Dim(truncate(l, u.width-14)))
			}
		}
	}
}

func (a *app) printReady(x *sessions.Session) {
	u := a.ui
	u.Printf("\n  %s %s\n\n", u.sym("ok"), u.bold.Render("Application ready"))
	u.Printf("  %s\n\n", u.Accent(x.URL))
	healthy := ""
	if _, ok := x.Milestones[sessions.MilestoneHealthy]; ok {
		healthy = " · healthy"
	}
	u.Printf("  %s %s%s\n", u.Dim("session"), u.Code(x.ID), u.Dim(healthy))
	u.Printf("  %s\n", u.Dim(fmt.Sprintf("logs: zerosetup logs %s -f · stop: zerosetup stop %s · passport: zerosetup export %s", x.ID, x.ID, x.ID)))
	u.Println()
}

func (a *app) printFailure(ctx context.Context, svc *engine.Service, x *sessions.Session) {
	if x == nil {
		return
	}
	diags, _ := svc.Diagnostics(context.WithoutCancel(ctx), x.ID)
	if len(diags) == 0 {
		return
	}
	u := a.ui
	u.Section("Diagnostics")
	for _, d := range diags {
		u.Printf("\n  %s %s %s\n", u.sym("bad"), u.bold.Render(d.Summary), u.Dim("("+d.ID+", confidence "+d.Confidence+")"))
		for _, ev := range d.Evidence {
			u.Printf("      %s %s\n", u.Dim("evidence:"), truncate(ev, u.width-20))
		}
		u.Printf("      %s %s\n", u.Dim("likely cause:"), d.LikelyCause)
		u.Printf("      %s %s\n", u.Dim("resolution:"), d.ProposedResolution)
		if d.Repair != nil {
			u.Printf("      %s %s → %s\n", u.Accent("repair available:"), d.Repair.Title,
				u.Code(fmt.Sprintf("zerosetup repair %s --apply %s", x.ID, d.Repair.ID)))
		}
	}
	u.Println()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}
