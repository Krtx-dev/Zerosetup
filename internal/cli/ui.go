package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// UI renders human-readable output. It degrades to plain text when color
// is disabled (NO_COLOR, --no-color, or a non-terminal).
type UI struct {
	out, err io.Writer
	color    bool
	width    int

	title, dim, bold, accent, ok, warn, bad, code lipgloss.Style
}

func newUI(out, errw io.Writer, noColor bool) *UI {
	u := &UI{out: out, err: errw, width: 100}
	f, isFile := out.(*os.File)
	tty := isFile && term.IsTerminal(int(f.Fd()))
	u.color = tty && !noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if tty {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 40 {
			u.width = w
		}
	}
	r := lipgloss.NewRenderer(out)
	if !u.color {
		r.SetColorProfile(termenv.Ascii)
	}
	u.title = r.NewStyle().Bold(true).Foreground(lipgloss.Color("#E6EDF3"))
	u.dim = r.NewStyle().Foreground(lipgloss.Color("#7D8590"))
	u.bold = r.NewStyle().Bold(true)
	u.accent = r.NewStyle().Foreground(lipgloss.Color("#56D4F5"))
	u.ok = r.NewStyle().Foreground(lipgloss.Color("#3FB950"))
	u.warn = r.NewStyle().Foreground(lipgloss.Color("#D29922"))
	u.bad = r.NewStyle().Foreground(lipgloss.Color("#F85149"))
	u.code = r.NewStyle().Foreground(lipgloss.Color("#C9D1D9"))
	return u
}

func (u *UI) sym(kind string) string {
	if !u.color {
		switch kind {
		case "ok":
			return "[ok]"
		case "warn":
			return "[!]"
		case "bad":
			return "[x]"
		case "run":
			return "[..]"
		}
		return "-"
	}
	switch kind {
	case "ok":
		return u.ok.Render("✓")
	case "warn":
		return u.warn.Render("!")
	case "bad":
		return u.bad.Render("✗")
	case "run":
		return u.accent.Render("›")
	}
	return u.dim.Render("·")
}

func (u *UI) Printf(format string, a ...any) { fmt.Fprintf(u.out, format, a...) }
func (u *UI) Println(a ...any)               { fmt.Fprintln(u.out, a...) }

func (u *UI) Header(sub string) {
	u.Printf("\n%s  %s\n\n", u.title.Render("ZEROSETUP"), u.dim.Render(sub))
}

func (u *UI) Section(name string) { u.Printf("\n  %s\n", u.bold.Render(name)) }

func (u *UI) Line(kind, msg string) { u.Printf("  %s %s\n", u.sym(kind), msg) }

// KV prints an aligned key/value row with an optional provenance tag.
func (u *UI) KV(key, val, kind, reason string) {
	tag := ""
	if kind != "" {
		st := u.dim
		switch kind {
		case "DETECTED":
			st = u.ok
		case "USER_OVERRIDE":
			st = u.accent
		case "UNSUPPORTED":
			st = u.bad
		case "DEFAULT":
			st = u.warn
		}
		tag = " " + st.Render(fmt.Sprintf("%-13s", kind))
	}
	u.Printf("  %s %s%s\n", u.dim.Render(fmt.Sprintf("%-14s", key)), u.code.Render(val), tag)
	if reason != "" {
		u.Printf("  %-14s %s\n", "", u.dim.Render(truncate(reason, u.width-20)))
	}
}

func truncate(s string, n int) string {
	if n < 20 {
		n = 20
	}
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func (u *UI) Code(s string) string   { return u.code.Render(s) }
func (u *UI) Dim(s string) string    { return u.dim.Render(s) }
func (u *UI) Accent(s string) string { return u.accent.Render(s) }
func (u *UI) Warn(s string) string   { return u.warn.Render(s) }
func (u *UI) Bad(s string) string    { return u.bad.Render(s) }
func (u *UI) Ok(s string) string     { return u.ok.Render(s) }

func indent(s, pre string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pre + l
	}
	return strings.Join(lines, "\n")
}
