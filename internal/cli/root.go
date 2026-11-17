// Package cli implements the zerosetup command-line interface. Commands are
// thin: all behaviour lives in internal/engine.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/zerosetup/zerosetup/internal/engine"
	"github.com/zerosetup/zerosetup/internal/passport"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/runtime/docker"
	"github.com/zerosetup/zerosetup/internal/store"
	"github.com/zerosetup/zerosetup/internal/zerr"
	"github.com/zerosetup/zerosetup/pkg/planschema"
)

// Exit codes (documented in docs/CLI.md).
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitUnsupported = 3
	ExitApproval    = 4
	ExitNoDocker    = 5
	ExitNotReady    = 6
	ExitCancelled   = 130
)

type app struct {
	ui      *UI
	jsonOut bool
	noColor bool
	svc     *engine.Service
	in      io.Reader
	out     io.Writer
}

func (a *app) service() (*engine.Service, error) {
	if a.svc != nil {
		return a.svc, nil
	}
	paths, err := engine.DefaultPaths()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		return nil, err
	}
	a.svc = engine.New(paths, st, func(ctx context.Context) (runtime.Backend, error) { return docker.New(ctx) })
	return a.svc, nil
}

func (a *app) emitJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// NewRoot builds the command tree.
func NewRoot(in io.Reader, out, errw io.Writer) (*cobra.Command, *app) {
	a := &app{in: in, out: out}
	root := &cobra.Command{
		Use:   "zerosetup",
		Short: "From GitHub URL to running application",
		Long: `ZEROSETUP inspects a repository without executing it, explains how it would
run it, and — only after you approve the exact plan — runs it in a hardened,
isolated container and verifies that it actually responds.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.ui = newUI(out, errw, a.noColor)
		},
	}
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "disable colored output (NO_COLOR is also honoured)")
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errw)
	root.AddCommand(
		a.cmdVersion(), a.cmdDoctor(), a.cmdInspect(), a.cmdPlan(), a.cmdRun(),
		a.cmdPs(), a.cmdStatus(), a.cmdLogs(), a.cmdStop(), a.cmdRestart(), a.cmdRemove(),
		a.cmdExport(), a.cmdReplay(), a.cmdRepair(), a.cmdDashboard(), a.cmdConfig(), a.cmdCache(),
	)
	return root, a
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string) int {
	root, a := NewRoot(os.Stdin, os.Stdout, os.Stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if a.svc != nil {
		a.svc.Close()
	}
	if err == nil {
		return ExitOK
	}
	if a.ui == nil {
		a.ui = newUI(os.Stdout, os.Stderr, true)
	}
	return a.reportError(err)
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withExit(code int, err error) error { return &exitError{code: code, err: err} }

func (a *app) reportError(err error) int {
	code := ExitError
	var ee *exitError
	if errors.As(err, &ee) {
		code = ee.code
	}
	ze, ok := zerr.As(err)
	if ok {
		switch ze.Code {
		case zerr.CodeDockerUnavailable:
			code = ExitNoDocker
		case zerr.CodeApprovalRequired, zerr.CodeApprovalMismatch:
			code = ExitApproval
		case zerr.CodeUnsupportedStack:
			code = ExitUnsupported
		case zerr.CodeCancelled:
			code = ExitCancelled
		case zerr.CodeNotReady, zerr.CodeStepFailed:
			if code == ExitError {
				code = ExitNotReady
			}
		}
	}
	if a.jsonOut {
		payload := map[string]any{"error": map[string]any{"code": "ZS-INTERNAL-001", "summary": err.Error()}}
		if ok {
			payload["error"] = map[string]any{"code": ze.Code, "stage": ze.Stage, "summary": ze.Summary, "detail": ze.Detail,
				"suggestion": ze.Suggestion, "cause": ze.CauseText()}
		}
		_ = json.NewEncoder(os.Stderr).Encode(payload)
		return code
	}
	u := newUI(os.Stderr, os.Stderr, a.noColor)
	if ok {
		u.Printf("\n  %s %s  %s\n", u.sym("bad"), u.Bad(string(ze.Code)), u.bold.Render(ze.Summary))
		if ze.Detail != "" {
			u.Printf("%s\n", indent(ze.Detail, "     "))
		}
		if ze.Cause != nil {
			u.Printf("     %s %s\n", u.Dim("cause:"), u.Dim(truncate(ze.Cause.Error(), 400)))
		}
		if ze.Suggestion != "" {
			u.Printf("\n     %s %s\n", u.Accent("→"), ze.Suggestion)
		}
		u.Println()
		return code
	}
	u.Printf("\n  %s %s\n\n", u.sym("bad"), err.Error())
	return code
}

func (a *app) cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(a.out, "zerosetup %s (plan schema %s, passport schema %s)\n", engine.Version, planschema.SchemaVersion, passport.SchemaVersion)
			return nil
		},
	}
}
