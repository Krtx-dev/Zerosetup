package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/sessions"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func stateKind(s sessions.State) string {
	switch s {
	case sessions.Ready:
		return "ok"
	case sessions.Failed, sessions.Unknown:
		return "bad"
	case sessions.Stopped, sessions.Cancelled, sessions.Removed, sessions.AwaitingApproval:
		return "info"
	}
	return "run"
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Local().Format("2006-01-02")
}

func (a *app) cmdPs() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List sessions (state is reconciled with the container engine)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			list, err := svc.List(cmd.Context(), all)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if list == nil {
					list = []*sessions.Session{}
				}
				return a.emitJSON(list)
			}
			u := a.ui
			if len(list) == 0 {
				u.Printf("\n  %s\n\n", u.Dim("No sessions. Start one with `zerosetup run <github-url>`."))
				return nil
			}
			u.Printf("\n  %-12s %-18s %-38s %-16s %-24s %s\n", u.Dim("SESSION"), u.Dim("STATE"), u.Dim("REPOSITORY"), u.Dim("FRAMEWORK"), u.Dim("URL"), u.Dim("CREATED"))
			for _, x := range list {
				repo := tildePath(x.Repository)
				if len(repo) > 38 {
					repo = "…" + repo[len(repo)-37:]
				}
				u.Printf("  %-12s %s %-16s %-38s %-16s %-24s %s\n", x.ID, u.sym(stateKind(x.State)), x.State, repo, truncate(x.Framework, 16), x.URL, u.Dim(ago(x.CreatedAt)))
			}
			u.Println()
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include removed sessions")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}

func (a *app) cmdStatus() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <session-id>",
		Short: "Show a session's state, milestones, readiness and diagnostics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			x, err := svc.Status(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			f := engine.FactsOf(x)
			if a.jsonOut {
				return a.emitJSON(map[string]any{"session": x, "facts": f})
			}
			u := a.ui
			u.Header("status " + x.ID)
			u.Line(stateKind(x.State), u.bold.Render(string(x.State))+"  "+u.Code(tildePath(x.Repository)))
			u.KV("Framework", x.Framework, "", "")
			u.KV("Runtime", x.Runtime, "", "")
			if x.Commit != "" {
				u.KV("Commit", x.Commit, "", "")
			}
			u.KV("Plan", ps.ShortHash(x.PlanHash), "", "")
			if x.URL != "" {
				u.KV("URL", x.URL, "", "")
			}
			if x.ImageDigest != "" {
				u.KV("Image", x.ImageDigest, "", "")
			}
			if len(x.Milestones) > 0 {
				u.Section("Milestones")
				type ms struct {
					m sessions.Milestone
					t time.Time
				}
				var list []ms
				for m, t := range x.Milestones {
					list = append(list, ms{m, t})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].t.Before(list[j].t) })
				for _, m := range list {
					u.Line("ok", fmt.Sprintf("%-24s %s", m.m, u.Dim(m.t.Local().Format("15:04:05.000"))))
				}
			}
			if len(f.Steps) > 0 {
				u.Section("Steps")
				for _, st := range f.Steps {
					if st.Skipped {
						u.Line("info", fmt.Sprintf("%-14s skipped (%s)", st.ID, st.SkipReason))
						continue
					}
					k := "ok"
					if st.ExitCode != 0 {
						k = "bad"
					}
					u.Line(k, fmt.Sprintf("%-14s exit %d in %s", st.ID, st.ExitCode, (time.Duration(st.DurationMs)*time.Millisecond).Round(100*time.Millisecond)))
				}
			}
			if r := f.Readiness; r != nil {
				u.Section("Readiness")
				var ls []string
				for _, l := range r.Listeners {
					ls = append(ls, l.String())
				}
				u.KV("Listening", strings.Join(ls, ", "), "", "")
				u.KV("HTTP", fmt.Sprintf("GET %s → %d after %d attempts (%d ms)", r.Path, r.LastStatus, r.Attempts, r.DurationMs), "", "")
				u.KV("Healthy", fmt.Sprint(r.Healthy), "", "")
			}
			if x.Error != nil {
				u.Section("Error")
				u.Line("bad", string(x.Error.Code)+"  "+x.Error.Summary)
			}
			if len(f.Diagnostics) > 0 {
				a.printFailure(cmd.Context(), svc, x)
			}
			u.Println()
			return nil
		},
	}
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}

func (a *app) cmdLogs() *cobra.Command {
	var follow bool
	var tail, step string
	cmd := &cobra.Command{
		Use:   "logs <session-id>",
		Short: "Print application or step logs",
		Example: `  zerosetup logs zs1a2b3c4d -f
  zerosetup logs zs1a2b3c4d --step install-deps`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			return svc.Logs(cmd.Context(), args[0], step, follow, tail, os.Stdout)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow application output")
	cmd.Flags().StringVar(&tail, "tail", "all", "number of lines from the end (application logs)")
	cmd.Flags().StringVar(&step, "step", "", "read a step's log (e.g. install-deps) instead of the application")
	return cmd
}

func (a *app) cmdStop() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop the application (workspace is kept for restart)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			x, err := svc.Stop(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			a.ui.Printf("\n  %s %s is %s\n\n", a.ui.sym("ok"), x.ID, x.State)
			return nil
		},
	}
}

func (a *app) cmdRestart() *cobra.Command {
	var f planFlags
	cmd := &cobra.Command{
		Use:   "restart <session-id>",
		Short: "Restart the application and re-verify readiness",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			x, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			plan, err := svc.Plan(cmd.Context(), x)
			if err != nil {
				return err
			}
			secrets, err := a.collectSecrets(plan, f.secrets, f.secretsEnv)
			if err != nil {
				return err
			}
			unsub := svc.Subscribe(a.progressPrinter(x.ID))
			x, err = svc.Restart(cmd.Context(), args[0], secrets)
			unsub()
			if err != nil {
				a.printFailure(cmd.Context(), svc, x)
				return err
			}
			a.printReady(x)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&f.secrets, "secret", nil, "secret NAME to prompt for")
	cmd.Flags().StringArrayVar(&f.secretsEnv, "secret-from-env", nil, "secret NAME read from your environment")
	return cmd
}

func (a *app) cmdRemove() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <session-id>",
		Aliases: []string{"rm"},
		Short:   "Remove all of a session's containers, volumes and networks, and verify",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			res, err := svc.Remove(cmd.Context(), args[0])
			if a.jsonOut && res != nil {
				_ = a.emitJSON(res)
			}
			if err != nil {
				return err
			}
			if !a.jsonOut {
				u := a.ui
				u.Printf("\n  %s Removed %d containers, %d volumes, %d networks\n", u.sym("ok"), len(res.Removed.Containers), len(res.Removed.Volumes), len(res.Removed.Networks))
				u.Printf("  %s Verified: no resources labelled with session %s remain\n\n", u.sym("ok"), args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}

func (a *app) cmdExport() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export the session's reproducibility passport (JSON, secret-free)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			b, _, err := svc.Passport(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if outPath == "" || outPath == "-" {
				_, err = a.out.Write(b)
				return err
			}
			if err := os.WriteFile(outPath, b, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "passport written to %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to a file instead of stdout")
	return cmd
}

func (a *app) cmdRepair() *cobra.Command {
	var apply string
	var revert bool
	cmd := &cobra.Command{
		Use:   "repair <session-id>",
		Short: "List, apply or revert proposed repairs to a failed session's plan",
		Long: `Repairs change ZEROSETUP's generated plan (port, environment, limits,
timeouts) — never the repository. Applying a repair creates a new plan that
must be approved before it runs. At most 3 repairs can be applied per session.`,
		Example: `  zerosetup repair zs1a2b3c4d
  zerosetup repair zs1a2b3c4d --apply bind-all-interfaces
  zerosetup repair zs1a2b3c4d --revert`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			u := a.ui
			ctx := cmd.Context()
			switch {
			case revert:
				x, err := svc.RevertRepair(ctx, args[0])
				if err != nil {
					return err
				}
				u.Printf("\n  %s Reverted to plan %s. Run it with: zerosetup run --session %s\n\n", u.sym("ok"), ps.ShortHash(x.PlanHash), x.ID)
			case apply != "":
				out, err := svc.ApplyRepair(ctx, args[0], apply)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return a.emitJSON(out)
				}
				u.Header("repair " + args[0])
				u.Line("ok", "Applied to the plan (repository unchanged): "+out.Repair.Title)
				for _, c := range out.Repair.Changes {
					key := c.Override
					if c.Key != "" {
						key += " " + c.Key
					}
					u.Printf("      %s: %s → %s\n", key, u.Dim(c.From), u.Code(c.To))
				}
				u.Printf("\n  plan %s → %s\n", u.Dim(ps.ShortHash(out.OldHash)), u.Accent(ps.ShortHash(out.NewHash)))
				u.Printf("  Run with approval: %s\n  Undo: %s\n\n",
					u.Code(fmt.Sprintf("zerosetup run --session %s", args[0])), u.Code(fmt.Sprintf("zerosetup repair %s --revert", args[0])))
			default:
				diags, err := svc.Diagnostics(ctx, args[0])
				if err != nil {
					return err
				}
				if a.jsonOut {
					return a.emitJSON(diags)
				}
				n := 0
				for _, d := range diags {
					if d.Repair == nil {
						continue
					}
					n++
					u.Printf("\n  %s %s\n      %s\n      %s %s\n", u.Accent(d.Repair.ID), u.bold.Render(d.Repair.Title), d.Repair.Explanation, u.Dim("impact:"), d.Repair.Impact)
				}
				if n == 0 {
					u.Printf("\n  %s\n\n", u.Dim("No repairs are proposed for this session."))
				} else {
					u.Println()
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&apply, "apply", "", "apply the repair with this ID to the plan")
	cmd.Flags().BoolVar(&revert, "revert", false, "revert the last applied repair")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}
