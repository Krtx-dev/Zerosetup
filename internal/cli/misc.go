package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zerosetup/zerosetup/internal/api"
	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/planner"
)

func (a *app) cmdDoctor() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that this machine can inspect and run repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			checks := svc.Doctor(cmd.Context())
			if a.jsonOut {
				return a.emitJSON(checks)
			}
			u := a.ui
			u.Header("doctor")
			failed := false
			for _, c := range checks {
				k := map[string]string{"ok": "ok", "warn": "warn", "fail": "bad"}[c.Status]
				failed = failed || c.Status == "fail"
				detail := c.Detail
				if h, err := os.UserHomeDir(); err == nil && h != "" {
					detail = strings.ReplaceAll(detail, h, "~")
				}
				u.Printf("  %s %-18s %s\n", u.sym(k), c.Name, detail)
				if c.Suggestion != "" {
					u.Printf("    %-18s %s\n", "", u.Dim("→ "+c.Suggestion))
				}
			}
			u.Println()
			if failed {
				return withExit(ExitNoDocker, fmt.Errorf("one or more checks failed"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	return cmd
}

func (a *app) cmdDashboard() *cobra.Command {
	var port int
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Start the local dashboard (bound to 127.0.0.1, token-protected)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			srv, err := api.New(svc, api.Options{Port: port})
			if err != nil {
				return err
			}
			url := srv.DashboardURL()
			u := a.ui
			u.Header("dashboard")
			u.Printf("  %s\n\n", u.Accent(url))
			u.Printf("  %s\n", u.Dim("The URL contains a one-time access token (in the #fragment, never sent over the network)."))
			u.Printf("  %s\n\n", u.Dim("Press Ctrl-C to stop. Running sessions keep running."))
			if !noOpen {
				openBrowser(url)
			}
			return srv.Serve(cmd.Context())
		},
	}
	cmd.Flags().IntVar(&port, "port", 4316, "loopback port for the dashboard (0 = random)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser")
	return cmd
}

func (a *app) cmdConfig() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show data locations and built-in defaults (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := engine.DefaultPaths()
			if err != nil {
				return err
			}
			cfg := map[string]any{
				"home": p.Home, "database": p.DB, "sourceCache": p.Sources, "sessionLogs": p.Logs,
				"defaults": map[string]any{"cpus": planner.DefaultCPUs, "memoryMB": planner.DefaultMemoryMB, "pidsLimit": planner.DefaultPidsLimit,
					"logMaxSizeMB": planner.DefaultLogMaxSizeMB, "readinessTimeoutSec": planner.DefaultStartTimeoutSec, "previewPorts": "127.0.0.1:4317+",
					"dashboardPort": 4316},
				"fixedContainerEnv": engine.FixedEnv(),
				"note":              "Set ZEROSETUP_HOME to relocate data. Per-run settings are flags on plan/run; there is no persistent config file yet.",
			}
			a.jsonOut = true
			return a.emitJSON(cfg)
		},
	}
}

func (a *app) cmdCache() *cobra.Command {
	cmd := &cobra.Command{Use: "cache", Short: "Inspect or clean ZEROSETUP-owned caches"}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show cache usage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			cs, err := svc.Cache(cmd.Context())
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.emitJSON(cs)
			}
			u := a.ui
			u.Header("cache")
			u.KV("Sources", fmt.Sprintf("%d checkouts, %.1f MiB in %s", cs.SourceDirs, float64(cs.SourceBytes)/(1<<20), cs.SourceRoot), "", "")
			vols := "none (or container engine unavailable)"
			if len(cs.Volumes) > 0 {
				vols = strings.Join(cs.Volumes, ", ")
			}
			u.KV("Pkg caches", vols, "", "")
			u.Println()
			return nil
		},
	}
	status.Flags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	var sources bool
	clean := &cobra.Command{
		Use:   "clean",
		Short: "Remove ZEROSETUP's package cache volumes (and, with --sources, cached checkouts)",
		Long:  "Only volumes labelled io.zerosetup.role=cache are removed. Nothing else on your Docker host is touched.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service()
			if err != nil {
				return err
			}
			cs, err := svc.CleanCache(cmd.Context(), sources)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "removed %d cache volumes", len(cs.Volumes))
			if sources {
				fmt.Fprintf(os.Stdout, " and %d source checkouts", cs.SourceDirs)
			}
			fmt.Fprintln(os.Stdout)
			return nil
		},
	}
	clean.Flags().BoolVar(&sources, "sources", false, "also delete cached source checkouts")
	cmd.AddCommand(status, clean)
	return cmd
}
