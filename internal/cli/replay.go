package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zerosetup/zerosetup/internal/engine"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

func (a *app) cmdReplay() *cobra.Command {
	var src string
	var noSession, checkImages bool
	cmd := &cobra.Command{
		Use:   "replay <passport.json>",
		Short: "Check a reproducibility passport against today and prepare a session to re-run it",
		Long: `replay re-resolves the recorded commit, regenerates the plan with the
recorded overrides, and compares commit, manifests, lockfiles, plan hash,
platform and (with --check-images) base-image digests. It never claims the
result will be identical. If no check fails, it creates a session that still
needs your approval: zerosetup run --session <id>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service()
			if err != nil {
				return err
			}
			rep, err := svc.Replay(cmd.Context(), data, engine.ReplayOptions{Source: src, CreateSession: !noSession, CheckImages: checkImages})
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.emitJSON(rep)
			}
			u := a.ui
			u.Header("replay")
			for _, c := range rep.Checks {
				k := map[string]string{"ok": "ok", "warn": "warn", "fail": "bad"}[c.Status]
				u.Printf("  %s %-22s %s\n", u.sym(k), c.Name, c.Detail)
			}
			if !rep.CanRun {
				u.Println()
				return withExit(ExitUnsupported, errors.New("replay checks failed; nothing was created"))
			}
			if rep.Session != nil {
				u.Printf("\n  Session %s is awaiting approval of plan %s.\n  Run it with: %s\n\n", u.Code(rep.Session.ID), u.Accent(ps.ShortHash(rep.PlanHash)),
					u.Code(fmt.Sprintf("zerosetup run --session %s", rep.Session.ID)))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&src, "source", "", "directory to replay from (required for passports of local sources)")
	cmd.Flags().BoolVar(&noSession, "check-only", false, "only run the checks; do not create a session")
	cmd.Flags().BoolVar(&checkImages, "check-images", false, "pull image tags and compare digests (needs Docker and network)")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}
