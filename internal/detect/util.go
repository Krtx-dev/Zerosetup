package detect

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"

	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// shellMeta are characters that mean a script needs a shell to interpret.
const shellMeta = "&|;<>$`'\"(){}*?~\\\n#!"

// SimpleScript splits a package.json script into argv if, and only if, it is
// a plain "<bin> <args...>" invocation that needs no shell semantics.
func SimpleScript(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, shellMeta) {
		return nil, false
	}
	f := strings.Fields(s)
	if strings.Contains(f[0], "=") { // leading VAR=value assignment
		return nil, false
	}
	return f, true
}

// StripFlags removes the given flags (and their values) from args.
// Handles "--flag value", "--flag=value" and bare "--flag".
func StripFlags(args []string, flags ...string) []string {
	drop := map[string]bool{}
	for _, f := range flags {
		drop[f] = true
	}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasEq := strings.Cut(a, "=")
		if drop[name] {
			if !hasEq && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// FlagValue returns the value of the first matching flag in args.
func FlagValue(args []string, flags ...string) (string, bool) {
	for i, a := range args {
		name, val, hasEq := strings.Cut(a, "=")
		for _, f := range flags {
			if name != f {
				continue
			}
			if hasEq {
				return val, true
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1], true
			}
		}
	}
	return "", false
}

var envLineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)
var secretNameRe = regexp.MustCompile(`(?i)(SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE|API_?KEY|ACCESS_?KEY|CREDENTIAL|_DSN$|DATABASE_URL|AUTH)`)

// LooksSecret reports whether an env var name conventionally holds a secret.
func LooksSecret(name string) bool { return secretNameRe.MatchString(name) }

// EnvTemplates are conventional environment template files.
var EnvTemplates = []string{".env.example", ".env.sample", ".env.template", ".env.dist", "example.env"}

// ReadEnvTemplates parses env template files into plan env vars. Values
// from templates are kept only for non-secret names; secret names are
// recorded without values so the user can supply them explicitly.
func ReadEnvTemplates(repo Repo, res *Result) {
	seen := map[string]bool{}
	for _, f := range EnvTemplates {
		data, err := repo.ReadFile(f, 256<<10)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			m := envLineRe.FindStringSubmatch(line)
			if m == nil || seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			name, val := m[1], strings.Trim(strings.TrimSpace(m[2]), `"'`)
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
			ev := ps.EnvVar{Name: name, Kind: ps.KindDetected, Reason: "listed in " + f}
			if LooksSecret(name) {
				ev.Secret = true
				ev.Reason += "; name suggests a secret, so no value is taken from the template"
			} else {
				ev.Value = val
			}
			res.Env = append(res.Env, ev)
		}
		if len(seen) > 0 {
			res.Decide("env", f, ps.KindDetected, ps.CertaintyDeclared,
				"environment variables listed in the repository's template", ps.Evidence{File: f, Detail: "environment template"})
			res.Warn("ENV_TEMPLATE", "The repository lists environment variables in "+f+". Placeholder values may need real values; secrets must be provided explicitly with --secret.", f)
		}
		return // only the first template found is used
	}
}

// MajorOf extracts the leading integer of a version-ish string.
func MajorOf(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	return v[:end]
}
