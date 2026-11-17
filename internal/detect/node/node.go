// Package node detects Node.js applications: Next.js, Vite (incl. React),
// Express and plain Node services, with npm, pnpm or yarn.
package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/zerosetup/zerosetup/internal/detect"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// DefaultMajor is used when the repository declares no Node.js version (ADR-009).
const DefaultMajor = 24

// SupportedMajors have official images and are accepted.
var SupportedMajors = map[int]bool{18: true, 20: true, 22: true, 24: true, 26: true}

// Default package manager versions when nothing pins them (ADR-008).
const (
	defaultPnpm  = "10"
	defaultYarn1 = "1.22.22"
)

type Detector struct{}

func New() *Detector { return &Detector{} }

func (*Detector) ID() string                     { return "node" }
func (*Detector) Priority() int                  { return 50 }
func (*Detector) Supports(repo detect.Repo) bool { return repo.Exists("package.json") }

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Main            string            `json:"main"`
	Type            string            `json:"type"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         struct {
		Node string `json:"node"`
	} `json:"engines"`
	Volta struct {
		Node string `json:"node"`
	} `json:"volta"`
	PackageManager string          `json:"packageManager"`
	Workspaces     json.RawMessage `json:"workspaces"`
}

func (p *packageJSON) has(dep string) (string, bool) {
	if v, ok := p.Dependencies[dep]; ok {
		return v, true
	}
	v, ok := p.DevDependencies[dep]
	return v, ok
}

func (d *Detector) Detect(ctx context.Context, repo detect.Repo, hints detect.Hints) (*detect.Result, error) {
	raw, err := repo.ReadFile("package.json", 0)
	if err != nil {
		return nil, nil
	}
	res := &detect.Result{Stack: ps.Stack{Language: "JavaScript/TypeScript", Runtime: "node"}, Score: 10}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		res.Unsupported = append(res.Unsupported, "package.json is not valid JSON: "+err.Error())
		return res, nil
	}
	pjEv := func(detail string) ps.Evidence { return ps.Evidence{File: "package.json", Detail: detail} }
	res.Decide("language", "JavaScript/TypeScript", ps.KindDetected, ps.CertaintyDeclared,
		"package.json exists at the repository root", pjEv("package manifest"))
	if repo.Exists("tsconfig.json") {
		res.Stack.Language = "TypeScript"
	}
	monorepo := len(pkg.Workspaces) > 0 && string(pkg.Workspaces) != "null" || repo.Exists("pnpm-workspace.yaml")

	d.detectNodeVersion(repo, &pkg, res, hints)
	pm := d.detectPackageManager(repo, &pkg, res)
	if pm == "" {
		return res, nil
	}
	target, tpkg := detect.Repo(repo), &pkg
	ws := ""
	if hints.Workspace != "" {
		ws = path.Clean(hints.Workspace)
		switch {
		case !monorepo:
			res.Unsupported = append(res.Unsupported, "--workspace was given, but package.json declares no workspaces")
			return res, nil
		case pm == "yarn" && res.Stack.PackageManagerVersion != "" && detect.MajorOf(res.Stack.PackageManagerVersion) != "1":
			res.Unsupported = append(res.Unsupported, "Yarn 2+ workspaces are not supported yet (npm, pnpm and Yarn 1 workspaces are)")
			return res, nil
		}
		b, err := repo.ReadFile(ws+"/package.json", 0)
		var wpkg packageJSON
		if err != nil || json.Unmarshal(b, &wpkg) != nil {
			res.Unsupported = append(res.Unsupported, fmt.Sprintf("workspace %q has no readable package.json", ws))
			return res, nil
		}
		if pm == "yarn" && wpkg.Version == "" {
			res.Unsupported = append(res.Unsupported, fmt.Sprintf("Yarn 1 ignores workspace %q because its package.json has no \"version\" field, so its dependencies would not be installed", ws))
			return res, nil
		}
		target, tpkg = detect.SubRepo{Repo: repo, Prefix: ws}, &wpkg
		res.Decide("workspace", ws, ps.KindOverride, ps.CertaintyDeclared, "workspace package selected with --workspace; dependencies are installed at the root",
			ps.Evidence{File: ws + "/package.json", Detail: "package " + wpkg.Name})
	} else if monorepo {
		cands := workspaceCandidates(repo)
		res.Warn("MONOREPO", "This is a monorepo. Only the root package is analysed; choose an app with --workspace <dir>"+
			candidateText(cands)+".", "package.json")
	}
	d.detectFrameworkAndStart(target, tpkg, pm, res, hints)
	if ws != "" {
		relocate(res, ws, pm != "pnpm")
	}
	if hints.Production() {
		res.Decide("mode", "production", ps.KindOverride, ps.CertaintyDeclared, "production mode requested with --mode production: build first, then serve the built output")
	}
	detect.ReadEnvTemplates(repo, res)
	detectServices(tpkg, res)
	return res, nil
}

// workspaceCandidates lists workspace packages that look like runnable apps.
func workspaceCandidates(repo detect.Repo) []string {
	var out []string
	for _, f := range repo.FindByName(3, "package.json") {
		if f == "package.json" || strings.Contains(f, "node_modules/") {
			continue
		}
		b, err := repo.ReadFile(f, 0)
		var p packageJSON
		if err != nil || json.Unmarshal(b, &p) != nil {
			continue
		}
		if has(&p, "vite") || has(&p, "next") || has(&p, "express") || p.Scripts["dev"] != "" || p.Scripts["start"] != "" {
			out = append(out, path.Dir(f))
		}
	}
	return out
}

func candidateText(c []string) string {
	if len(c) == 0 {
		return ""
	}
	return " (candidates: " + strings.Join(c, ", ") + ")"
}

// relocate moves start and build commands into the workspace directory.
// npm and Yarn 1 hoist workspace binaries to the root node_modules/.bin, so
// framework binaries are referenced relative to the workspace directory;
// pnpm links them into the workspace's own node_modules/.bin.
func relocate(res *detect.Result, ws string, hoisted bool) {
	up := ""
	if hoisted {
		up = strings.Repeat("../", strings.Count(ws, "/")+1)
	}
	fix := func(c *ps.Command) {
		c.Workdir = ws
		if len(c.Argv) > 0 && strings.HasPrefix(c.Argv[0], "node_modules/.bin/") {
			c.Argv[0] = up + c.Argv[0]
		}
	}
	fix(&res.Start)
	for i := range res.Steps {
		if res.Steps[i].Stage == ps.StageBuild {
			fix(&res.Steps[i].Command)
		}
	}
}

var majorRe = regexp.MustCompile(`\d+`)

// resolveNodeRange turns a semver-ish range into a supported major and a
// certainty. It is conservative: anything it cannot interpret yields the
// default with a warning rather than a guess presented as fact.
func resolveNodeRange(spec string) (major int, exact bool, ok bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(spec), "v"))
	if s == "" {
		return 0, false, false
	}
	if strings.HasPrefix(s, "lts") || s == "node" || s == "latest" || s == "*" {
		return DefaultMajor, false, true
	}
	if strings.Contains(s, "||") {
		best := 0
		for _, alt := range strings.Split(s, "||") {
			if m, _, ok := resolveNodeRange(alt); ok && m > best {
				best = m
			}
		}
		return best, false, best > 0
	}
	first := majorRe.FindString(s)
	n, err := strconv.Atoi(first)
	if err != nil {
		return 0, false, false
	}
	switch {
	case strings.HasPrefix(s, ">="), strings.HasPrefix(s, ">"):
		if DefaultMajor >= n {
			return DefaultMajor, false, true
		}
		return n, false, true
	case strings.HasPrefix(s, "<"):
		return 0, false, false
	default: // ^N, ~N, N, N.x, N.M.P
		return n, !strings.ContainsAny(s, "^~x*"), true
	}
}

// satisfiesNode reports whether major m satisfies a version spec. Specs it
// cannot interpret are treated as satisfied (no conflict is claimed).
func satisfiesNode(m int, spec string) bool {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(spec), "v"))
	if strings.Contains(s, "||") {
		for _, alt := range strings.Split(s, "||") {
			if satisfiesNode(m, alt) {
				return true
			}
		}
		return false
	}
	n, err := strconv.Atoi(majorRe.FindString(s))
	if err != nil || strings.HasPrefix(s, "lts") || s == "*" || s == "node" {
		return true
	}
	upper := -1
	if parts := strings.Fields(strings.ReplaceAll(s, ",", " ")); len(parts) == 2 && strings.HasPrefix(parts[1], "<") {
		if u, err := strconv.Atoi(majorRe.FindString(parts[1])); err == nil {
			upper = u
		}
	}
	switch {
	case strings.HasPrefix(s, ">="):
		return m >= n && (upper < 0 || m < upper)
	case strings.HasPrefix(s, ">"):
		return m > n && (upper < 0 || m < upper)
	case strings.HasPrefix(s, "<="):
		return m <= n
	case strings.HasPrefix(s, "<"):
		return m < n
	}
	return m == n
}

func (d *Detector) detectNodeVersion(repo detect.Repo, pkg *packageJSON, res *detect.Result, hints detect.Hints) {
	type signal struct{ file, value string }
	var sigs []signal
	for _, f := range []string{".nvmrc", ".node-version"} {
		if b, err := repo.ReadFile(f, 1024); err == nil {
			if v := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]); v != "" {
				sigs = append(sigs, signal{f, v})
			}
		}
	}
	if pkg.Volta.Node != "" {
		sigs = append(sigs, signal{"package.json#volta.node", pkg.Volta.Node})
	}
	if pkg.Engines.Node != "" {
		sigs = append(sigs, signal{"package.json#engines.node", pkg.Engines.Node})
	}

	major, kind, cert, reason := DefaultMajor, ps.KindDefault, ps.CertaintyLow, fmt.Sprintf("no Node.js version declared; using the documented default (Node.js %d LTS)", DefaultMajor)
	var ev []ps.Evidence
	chosen := -1
	for i, s := range sigs {
		m, _, ok := resolveNodeRange(s.value)
		file := strings.SplitN(s.file, "#", 2)[0]
		if !ok {
			res.Warn("NODE_VERSION_UNPARSED", fmt.Sprintf("Could not interpret Node.js version %q from %s", s.value, s.file), file)
			continue
		}
		ev = append(ev, ps.Evidence{File: file, Detail: fmt.Sprintf("%s declares %q", s.file, s.value)})
		if chosen < 0 {
			chosen = i
			major, kind, cert = m, ps.KindDetected, ps.CertaintyDeclared
			reason = fmt.Sprintf("%s declares %q", s.file, s.value)
		}
	}
	// A conflict exists only if the chosen version violates another
	// declaration (e.g. .nvmrc=18 with engines >=20), not merely because
	// two files are phrased differently.
	if chosen >= 0 {
		var bad []string
		for i, s := range sigs {
			if i != chosen && !satisfiesNode(major, s.value) {
				bad = append(bad, fmt.Sprintf("%s=%q", s.file, s.value))
			}
		}
		if len(bad) > 0 {
			res.Conflicts = append(res.Conflicts, ps.Conflict{Field: "runtimeVersion",
				Signals:    append([]string{fmt.Sprintf("%s=%q (selected: Node.js %d)", sigs[chosen].file, sigs[chosen].value, major)}, bad...),
				Resolution: "highest-precedence file wins (.nvmrc > .node-version > volta > engines); override with --runtime-version"})
		}
	}
	if hints.RuntimeVersion != "" {
		if m, err := strconv.Atoi(detect.MajorOf(hints.RuntimeVersion)); err == nil {
			major, kind, cert, reason = m, ps.KindOverride, ps.CertaintyDeclared, "set by --runtime-version"
		}
	}
	if !SupportedMajors[major] {
		res.Unsupported = append(res.Unsupported, fmt.Sprintf("Node.js %d is not supported (supported: 18, 20, 22, 24, 26)", major))
	} else if major == 18 {
		res.Warn("NODE_EOL", "Node.js 18 is end-of-life; it is used because the repository requests it.", "")
	}
	if kind == ps.KindDefault {
		res.Warn("NODE_VERSION_DEFAULT", reason, "")
	}
	res.Stack.RuntimeVersion = strconv.Itoa(major)
	res.Image = fmt.Sprintf("docker.io/library/node:%d-slim", major)
	res.Decide("runtimeVersion", res.Stack.RuntimeVersion, kind, cert, reason, ev...)
}

var pmFieldRe = regexp.MustCompile(`^(npm|pnpm|yarn|bun)@([0-9][0-9A-Za-z.\-]*)`)

func (d *Detector) detectPackageManager(repo detect.Repo, pkg *packageJSON, res *detect.Result) string {
	locks := map[string]string{
		"package-lock.json": "npm", "npm-shrinkwrap.json": "npm",
		"pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "bun.lockb": "bun", "bun.lock": "bun",
	}
	var found []string
	var lockFiles []string
	pms := map[string]bool{}
	for f, pm := range locks {
		if repo.Exists(f) {
			lockFiles = append(lockFiles, f)
			if !pms[pm] {
				pms[pm] = true
				found = append(found, pm)
			}
		}
	}
	sort.Strings(lockFiles)
	sort.Strings(found)

	var fieldPM, fieldVer string
	if m := pmFieldRe.FindStringSubmatch(pkg.PackageManager); m != nil {
		fieldPM, fieldVer = m[1], m[2]
	}

	pm, kind, cert, reason := "npm", ps.KindDefault, ps.CertaintyMedium, "no lockfile or packageManager field; npm is the Node.js default"
	var ev []ps.Evidence
	switch {
	case fieldPM != "":
		pm, kind, cert, reason = fieldPM, ps.KindDetected, ps.CertaintyDeclared, fmt.Sprintf("package.json declares packageManager %q", pkg.PackageManager)
		ev = append(ev, ps.Evidence{File: "package.json", Detail: reason})
		if len(found) > 0 && !pms[fieldPM] {
			res.Conflicts = append(res.Conflicts, ps.Conflict{Field: "packageManager",
				Signals:    append([]string{"packageManager=" + fieldPM}, lockFiles...),
				Resolution: "packageManager field wins; the lockfile for " + fieldPM + " is missing, so installs are not reproducible"})
		}
	case len(found) == 1:
		pm, kind, cert = found[0], ps.KindDetected, ps.CertaintyHigh
		reason = fmt.Sprintf("%s selected because %s exists", pm, strings.Join(lockFiles, ", "))
		for _, f := range lockFiles {
			ev = append(ev, ps.Evidence{File: f, Detail: "lockfile"})
		}
	case len(found) > 1:
		res.Conflicts = append(res.Conflicts, ps.Conflict{Field: "packageManager", Signals: lockFiles,
			Resolution: "not resolved automatically; remove the stale lockfile or add a packageManager field to package.json"})
		res.Unsupported = append(res.Unsupported, "multiple lockfiles for different package managers ("+strings.Join(lockFiles, ", ")+")")
		res.Decide("packageManager", strings.Join(found, "|"), ps.KindUnsupported, ps.CertaintyLow, "conflicting lockfiles")
		return ""
	}
	if pm == "bun" {
		res.Unsupported = append(res.Unsupported, "the Bun runtime/package manager is not supported yet")
		res.Decide("packageManager", "bun", ps.KindUnsupported, cert, reason, ev...)
		return ""
	}
	res.Stack.PackageManager = pm
	res.Decide("packageManager", pm, kind, cert, reason, ev...)

	tools := ps.Step{ID: "install-pm", Name: "Install " + pm, Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 300}
	switch pm {
	case "npm":
		res.Stack.PackageManagerVersion = "bundled"
		if repo.Exists("package-lock.json") || repo.Exists("npm-shrinkwrap.json") {
			res.Steps = append(res.Steps, installStep([]string{"npm", "ci", "--no-audit", "--no-fund"}, "lockfile present: npm ci installs exactly the locked versions"))
		} else {
			res.Steps = append(res.Steps, installStep([]string{"npm", "install", "--no-audit", "--no-fund"}, "no lockfile: npm install resolves versions now (not reproducible)"))
			res.Warn("NO_LOCKFILE", "No lockfile found; dependency versions are resolved at install time and may differ between runs.", "package.json")
		}
	case "pnpm":
		ver := fieldVer
		if ver == "" {
			ver = pnpmFromLockfile(repo)
		}
		res.Stack.PackageManagerVersion = ver
		tools.Command = ps.Command{Argv: []string{"npm", "install", "--global", "--no-audit", "--no-fund", "pnpm@" + ver}}
		tools.Reason = "pnpm is not bundled with Node.js; installed into the workspace (no root needed)"
		res.Steps = append(res.Steps, tools)
		if repo.Exists("pnpm-lock.yaml") {
			res.Steps = append(res.Steps, installStep([]string{"pnpm", "install", "--frozen-lockfile"}, "pnpm-lock.yaml present: frozen install"))
		} else {
			res.Steps = append(res.Steps, installStep([]string{"pnpm", "install"}, "no pnpm-lock.yaml: versions resolved at install time"))
			res.Warn("NO_LOCKFILE", "No pnpm-lock.yaml found; installs are not reproducible.", "package.json")
		}
	case "yarn":
		berry := repo.Exists(".yarnrc.yml") || (fieldVer != "" && detect.MajorOf(fieldVer) != "1")
		if berry {
			ver := fieldVer
			if ver == "" {
				ver = "4"
			}
			res.Stack.PackageManagerVersion = ver
			tools.Command = ps.Command{Argv: []string{"npm", "install", "--global", "--no-audit", "--no-fund", "@yarnpkg/cli-dist@" + ver}}
			tools.Reason = "Yarn Berry CLI installed into the workspace"
			// Plug'n'Play (the Berry default) has no node_modules/.bin, which
			// the start commands rely on, so only the node-modules linker is supported.
			var rc struct {
				NodeLinker string `yaml:"nodeLinker"`
			}
			if b, err := repo.ReadFile(".yarnrc.yml", 64<<10); err == nil {
				_ = yaml.Unmarshal(b, &rc)
			}
			if rc.NodeLinker != "node-modules" {
				linker := rc.NodeLinker
				if linker == "" {
					linker = "pnp (default)"
				}
				res.Unsupported = append(res.Unsupported, "Yarn 2+ with nodeLinker "+linker+" (Plug'n'Play) is not supported; set `nodeLinker: node-modules` in .yarnrc.yml")
				res.Decide("packageManager", "yarn "+ver, ps.KindUnsupported, ps.CertaintyHigh, "Yarn Plug'n'Play is not supported", ps.Evidence{File: ".yarnrc.yml", Detail: "nodeLinker: " + linker})
				return ""
			}
			res.Steps = append(res.Steps, tools, installStep([]string{"yarn", "install", "--immutable"}, "Yarn Berry immutable install (nodeLinker: node-modules)"))
		} else {
			ver := fieldVer
			if ver == "" {
				ver = defaultYarn1
			}
			res.Stack.PackageManagerVersion = ver
			tools.Command = ps.Command{Argv: []string{"npm", "install", "--global", "--no-audit", "--no-fund", "yarn@" + ver}}
			tools.Reason = "Yarn 1 installed into the workspace"
			args := []string{"yarn", "install", "--non-interactive"}
			if repo.Exists("yarn.lock") {
				args = append(args, "--frozen-lockfile")
			}
			res.Steps = append(res.Steps, tools, installStep(args, "Yarn 1 install"))
		}
	}
	return pm
}

func installStep(argv []string, reason string) ps.Step {
	return ps.Step{ID: "install-deps", Name: "Install dependencies", Stage: ps.StageInstall,
		Command: ps.Command{Argv: argv}, NeedsNetwork: true, TimeoutSec: 900, Reason: reason}
}

func pnpmFromLockfile(repo detect.Repo) string {
	b, err := repo.ReadFile("pnpm-lock.yaml", 64<<10*1024)
	if err != nil {
		return defaultPnpm
	}
	var head struct {
		LockfileVersion any `yaml:"lockfileVersion"`
	}
	// Only the first few lines matter; avoid parsing a huge document.
	if i := strings.Index(string(b), "\n\n"); i > 0 {
		b = b[:i]
	}
	if yaml.Unmarshal(b, &head) != nil {
		return defaultPnpm
	}
	switch detect.MajorOf(fmt.Sprint(head.LockfileVersion)) {
	case "5":
		return "7"
	case "6":
		return "8"
	default:
		return defaultPnpm
	}
}

func runScriptArgv(pm, script string) []string {
	if pm == "npm" {
		return []string{"npm", "run", script}
	}
	return []string{pm, "run", script}
}

func (d *Detector) detectFrameworkAndStart(repo detect.Repo, pkg *packageJSON, pm string, res *detect.Result, hints detect.Hints) {
	ev := func(detail string) ps.Evidence { return ps.Evidence{File: "package.json", Detail: detail} }
	switch {
	case has(pkg, "next"):
		v, _ := pkg.has("next")
		res.Stack.Framework = "Next.js"
		res.Score = 40
		res.Decide("framework", "Next.js", ps.KindDetected, ps.CertaintyDeclared, "package.json declares next as a dependency", ev("next@"+v))
		d.frameworkStart(repo, pkg, pm, res, hints, frameworkSpec{
			bin: "next", sub: []string{"dev"}, script: "dev", defaultPort: 3000,
			prodBuild: []string{"build"}, prodStart: []string{"start"}, prodPort: 3000,
			hostFlags: []string{"-H", "--hostname"}, portFlags: []string{"-p", "--port"},
			bindArgs: func(port int) []string { return []string{"-H", "0.0.0.0", "-p", strconv.Itoa(port)} },
		})
		res.Readiness.Path = "/"
	case has(pkg, "vite"):
		v, _ := pkg.has("vite")
		name := "Vite"
		if has(pkg, "react") {
			name = "React (Vite)"
		} else if has(pkg, "vue") {
			name = "Vue (Vite)"
		} else if has(pkg, "svelte") {
			name = "Svelte (Vite)"
		}
		res.Stack.Framework = name
		res.Score = 35
		res.Decide("framework", name, ps.KindDetected, ps.CertaintyDeclared, "package.json declares vite as a dependency", ev("vite@"+v))
		spec := frameworkSpec{
			bin: "vite", script: "dev", defaultPort: 5173,
			prodBuild: []string{"build"}, prodStart: []string{"preview"}, prodPort: 4173,
			hostFlags: []string{"--host"}, portFlags: []string{"--port"}, dropFlags: []string{"--strictPort", "--open"},
			bindArgs: func(port int) []string {
				return []string{"--host", "0.0.0.0", "--port", strconv.Itoa(port), "--strictPort"}
			},
		}
		if p, file := vitePortFromConfig(repo); p > 0 {
			spec.configPort, spec.configFile = p, file
		}
		d.frameworkStart(repo, pkg, pm, res, hints, spec)
		res.Readiness.Path = "/"
	case has(pkg, "express"):
		v, _ := pkg.has("express")
		res.Stack.Framework = "Express"
		res.Score = 30
		res.Decide("framework", "Express", ps.KindDetected, ps.CertaintyDeclared, "package.json declares express as a dependency", ev("express@"+v))
		d.serviceStart(repo, pkg, pm, res, hints)
	default:
		res.Stack.Framework = "Node.js"
		res.Score = 15
		res.Decide("framework", "Node.js (no recognised web framework)", ps.KindInferred, ps.CertaintyMedium,
			"no recognised framework dependency; treating as a plain Node.js service")
		d.serviceStart(repo, pkg, pm, res, hints)
	}
	if res.Readiness.Path == "" {
		res.Readiness.Path = "/"
	}
}

func has(pkg *packageJSON, dep string) bool { _, ok := pkg.has(dep); return ok }

type frameworkSpec struct {
	bin         string
	sub         []string
	script      string
	defaultPort int
	hostFlags   []string
	portFlags   []string
	dropFlags   []string
	bindArgs    func(port int) []string
	configPort  int
	configFile  string
	// Production mode: `<bin> <prodBuild>` then `<bin> <prodStart> <bindArgs>`.
	prodBuild []string
	prodStart []string
	prodPort  int
}

// frameworkStart builds the start command for a framework dev server whose
// binary accepts host/port flags (ADR-007).
func (d *Detector) frameworkStart(repo detect.Repo, pkg *packageJSON, pm string, res *detect.Result, hints detect.Hints, fs frameworkSpec) {
	if hints.Production() {
		d.frameworkProduction(pkg, res, hints, fs)
		return
	}
	scriptName := fs.script
	if hints.Script != "" {
		scriptName = hints.Script
	}
	script, hasScript := pkg.Scripts[scriptName]
	if hints.Script != "" && !hasScript {
		res.Unsupported = append(res.Unsupported, fmt.Sprintf("script %q (from --script) does not exist in package.json", hints.Script))
		return
	}
	// Port precedence: override > script flag > framework config > default.
	port, pkind, pcert, preason := fs.defaultPort, ps.KindInferred, ps.CertaintyHigh, fmt.Sprintf("%s dev server default port", res.Stack.Framework)
	var pev []ps.Evidence
	if fs.configPort > 0 {
		port, pkind, pcert, preason = fs.configPort, ps.KindDetected, ps.CertaintyMedium, "server.port set in "+fs.configFile
		pev = append(pev, ps.Evidence{File: fs.configFile, Detail: fmt.Sprintf("port: %d", fs.configPort)})
	}

	argv, simple := detect.SimpleScript(script)
	if hasScript && simple && argv[0] == fs.bin && hasPrefixArgs(argv[1:], fs.sub) {
		args := argv[1+len(fs.sub):]
		if v, ok := detect.FlagValue(args, fs.portFlags...); ok {
			if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
				port, pkind, pcert, preason = p, ps.KindDetected, ps.CertaintyDeclared, fmt.Sprintf("scripts.%s passes %s", scriptName, v)
				pev = []ps.Evidence{{File: "package.json", Detail: fmt.Sprintf("scripts.%s = %q", scriptName, script)}}
			}
		}
		port = d.applyPortHint(hints, port, &pkind, &pcert, &preason)
		args = detect.StripFlags(args, append(append(append([]string{}, fs.hostFlags...), fs.portFlags...), fs.dropFlags...)...)
		full := append([]string{"node_modules/.bin/" + fs.bin}, fs.sub...)
		full = append(full, args...)
		full = append(full, fs.bindArgs(port)...)
		res.Start = ps.Command{Argv: full}
		res.Decide("start", strings.Join(full, " "), ps.KindInferred, ps.CertaintyHigh,
			fmt.Sprintf("scripts.%s is a plain %q invocation; running the binary directly so the server binds 0.0.0.0 inside the container", scriptName, fs.bin),
			ps.Evidence{File: "package.json", Detail: fmt.Sprintf("scripts.%s = %q", scriptName, script)})
	} else if hasScript {
		port = d.applyPortHint(hints, port, &pkind, &pcert, &preason)
		res.Start = ps.Command{Argv: runScriptArgv(pm, scriptName)}
		res.Env = append(res.Env,
			ps.EnvVar{Name: "HOST", Value: "0.0.0.0", Kind: ps.KindInferred, Reason: "ask the dev server to bind all interfaces"},
			ps.EnvVar{Name: "PORT", Value: strconv.Itoa(port), Kind: ps.KindInferred, Reason: "conventional port variable"})
		res.Decide("start", strings.Join(res.Start.Argv, " "), ps.KindDetected, ps.CertaintyMedium,
			fmt.Sprintf("scripts.%s is not a plain %s invocation, so it is run through %s", scriptName, fs.bin, pm),
			ps.Evidence{File: "package.json", Detail: fmt.Sprintf("scripts.%s = %q", scriptName, script)})
		res.Warn("BIND_NOT_GUARANTEED", fmt.Sprintf("scripts.%s is complex; ZEROSETUP sets HOST/PORT but cannot guarantee the server binds 0.0.0.0. Diagnostics will detect a loopback-only bind.", scriptName), "package.json")
	} else {
		port = d.applyPortHint(hints, port, &pkind, &pcert, &preason)
		full := append([]string{"node_modules/.bin/" + fs.bin}, fs.sub...)
		full = append(full, fs.bindArgs(port)...)
		res.Start = ps.Command{Argv: full}
		res.Decide("start", strings.Join(full, " "), ps.KindInferred, ps.CertaintyMedium,
			fmt.Sprintf("no %q script; running the %s dev server directly", scriptName, fs.bin))
	}
	res.Port = port
	res.Decide("port", strconv.Itoa(port), pkind, pcert, preason, pev...)
}

func (d *Detector) applyPortHint(h detect.Hints, port int, k *ps.Kind, c *ps.Certainty, r *string) int {
	if h.Port > 0 {
		*k, *c, *r = ps.KindOverride, ps.CertaintyDeclared, "set by --port"
		return h.Port
	}
	return port
}

func hasPrefixArgs(args, prefix []string) bool {
	if len(prefix) == 0 {
		// `vite`, `vite dev` and `vite serve` all start the dev server.
		return len(args) == 0 || strings.HasPrefix(args[0], "-") || args[0] == "dev" || args[0] == "serve"
	}
	if len(args) < len(prefix) {
		return false
	}
	for i := range prefix {
		if args[i] != prefix[i] {
			return false
		}
	}
	return true
}

var vitePortRe = regexp.MustCompile(`(?s)server\s*:\s*\{[^}]*?\bport\s*:\s*(\d{2,5})`)

func vitePortFromConfig(repo detect.Repo) (int, string) {
	for _, f := range []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.mts", "vite.config.cjs"} {
		b, err := repo.ReadFile(f, 256<<10)
		if err != nil {
			continue
		}
		if m := vitePortRe.FindSubmatch(b); m != nil {
			if p, err := strconv.Atoi(string(m[1])); err == nil && p > 0 && p < 65536 {
				return p, f
			}
		}
		return 0, ""
	}
	return 0, ""
}

var (
	envPortRe    = regexp.MustCompile(`process\.env\.PORT\s*(?:\|\||\?\?)\s*['"]?(\d{2,5})`)
	listenPortRe = regexp.MustCompile(`\.listen\(\s*(\d{2,5})`)
	portConstRe  = regexp.MustCompile(`(?i)\bconst\s+port\s*=\s*['"]?(\d{2,5})`)
)

// serviceStart handles Express and plain Node services, which do not take
// host/port flags: they are started via their start script or entry file
// and receive PORT through the environment.
func (d *Detector) serviceStart(repo detect.Repo, pkg *packageJSON, pm string, res *detect.Result, hints detect.Hints) {
	var entry string
	var startEv ps.Evidence
	names := []string{"start", "dev"}
	if hints.Script != "" {
		names = []string{hints.Script}
	}
	for _, name := range names {
		script, ok := pkg.Scripts[name]
		if !ok {
			continue
		}
		startEv = ps.Evidence{File: "package.json", Detail: fmt.Sprintf("scripts.%s = %q", name, script)}
		if argv, simple := detect.SimpleScript(script); simple && argv[0] == "node" && len(argv) >= 2 {
			res.Start = ps.Command{Argv: argv}
			entry = argv[len(argv)-1]
			res.Decide("start", strings.Join(argv, " "), ps.KindDetected, ps.CertaintyDeclared,
				fmt.Sprintf("scripts.%s is a plain node invocation; run directly", name), startEv)
		} else {
			res.Start = ps.Command{Argv: runScriptArgv(pm, name)}
			res.Decide("start", strings.Join(res.Start.Argv, " "), ps.KindDetected, ps.CertaintyDeclared,
				fmt.Sprintf("scripts.%s declared in package.json", name), startEv)
		}
		break
	}
	if len(res.Start.Argv) == 0 && hints.Script != "" {
		res.Unsupported = append(res.Unsupported, fmt.Sprintf("script %q (from --script) does not exist in package.json", hints.Script))
		return
	}
	if len(res.Start.Argv) == 0 {
		candidates := []string{}
		if pkg.Main != "" {
			candidates = append(candidates, pkg.Main)
		}
		candidates = append(candidates, "server.js", "index.js", "app.js", "src/server.js", "src/index.js")
		for _, c := range candidates {
			if repo.Exists(c) {
				entry = c
				res.Start = ps.Command{Argv: []string{"node", c}}
				reason := "no start script; " + c + " is a conventional entry point"
				if c == pkg.Main {
					reason = "no start script; package.json main points to " + c
				}
				res.Decide("start", "node "+c, ps.KindInferred, ps.CertaintyMedium, reason, ps.Evidence{File: c, Detail: "entry file exists"})
				res.Warn("START_INFERRED", "No start script in package.json; the start command was inferred from "+c+".", "package.json")
				break
			}
		}
	}
	if len(res.Start.Argv) == 0 {
		res.Unsupported = append(res.Unsupported, "no start script in package.json and no conventional entry file (server.js, index.js, app.js)")
		res.Decide("start", "", ps.KindUnsupported, ps.CertaintyHigh, "missing start script")
		return
	}

	if hints.Production() {
		if _, ok := pkg.Scripts["build"]; ok {
			res.Steps = append(res.Steps, ps.Step{ID: "build", Name: "Build application", Stage: ps.StageBuild,
				Command: ps.Command{Argv: runScriptArgv(pm, "build")}, NeedsNetwork: true, TimeoutSec: 900, Reason: "production mode: scripts.build"})
		}
	}
	port, kind, cert, reason := 3000, ps.KindDefault, ps.CertaintyLow, "no port found in source; using the common Node.js default 3000 and setting PORT"
	var pev []ps.Evidence
	if entry == "" {
		for _, c := range []string{pkg.Main, "server.js", "index.js", "app.js", "src/server.js", "src/index.js"} {
			if c != "" && repo.Exists(c) {
				entry = c
				break
			}
		}
	}
	if entry != "" {
		if b, err := repo.ReadFile(entry, 256<<10); err == nil {
			for _, re := range []*regexp.Regexp{envPortRe, listenPortRe, portConstRe} {
				if m := re.FindSubmatch(b); m != nil {
					if p, err := strconv.Atoi(string(m[1])); err == nil && p > 0 && p < 65536 {
						port, kind, cert = p, ps.KindInferred, ps.CertaintyMedium
						reason = fmt.Sprintf("port %d found in %s", p, entry)
						pev = append(pev, ps.Evidence{File: entry, Detail: strings.TrimSpace(string(m[0]))})
						break
					}
				}
			}
		}
	}
	if hints.Port > 0 {
		port, kind, cert, reason = hints.Port, ps.KindOverride, ps.CertaintyDeclared, "set by --port"
	}
	if kind == ps.KindDefault {
		res.Warn("PORT_DEFAULT", reason, "")
	}
	res.Port = port
	res.Env = append(res.Env, ps.EnvVar{Name: "PORT", Value: strconv.Itoa(port), Kind: ps.KindInferred, Reason: "Node.js services conventionally read PORT"})
	res.Decide("port", strconv.Itoa(port), kind, cert, reason, pev...)
	res.Readiness.Path = "/"
}

// detectServices reports (never provisions) likely backing services.
func detectServices(pkg *packageJSON, res *detect.Result) {
	add := func(kind, dep string) {
		res.Services = append(res.Services, detect.ServiceHint{Kind: kind, Evidence: ps.Evidence{File: "package.json", Detail: "depends on " + dep}})
	}
	for _, dep := range []string{"pg", "postgres", "@prisma/client", "pg-promise"} {
		if has(pkg, dep) {
			add("postgres", dep)
			break
		}
	}
	for _, dep := range []string{"redis", "ioredis"} {
		if has(pkg, dep) {
			add("redis", dep)
			break
		}
	}
	if len(res.Services) > 0 {
		res.Warn("SERVICES_NOT_PROVISIONED", "Dependencies suggest backing services (e.g. a database). They are not started automatically; add --with-service postgres / --with-service redis if the app needs them.", "package.json")
	}
}

// frameworkProduction builds with the framework binary and serves the build.
func (d *Detector) frameworkProduction(pkg *packageJSON, res *detect.Result, hints detect.Hints, fs frameworkSpec) {
	port, kind, reason := fs.prodPort, ps.KindInferred, fmt.Sprintf("%s production server default port", fs.bin)
	if hints.Port > 0 {
		port, kind, reason = hints.Port, ps.KindOverride, "set by --port"
	}
	build := append([]string{"node_modules/.bin/" + fs.bin}, fs.prodBuild...)
	res.Steps = append(res.Steps, ps.Step{ID: "build", Name: "Build application", Stage: ps.StageBuild, Command: ps.Command{Argv: build},
		NeedsNetwork: true, TimeoutSec: 900, Reason: "production mode: `" + strings.Join(append([]string{fs.bin}, fs.prodBuild...), " ") + "`"})
	if sc, ok := pkg.Scripts["build"]; ok {
		if argv, simple := detect.SimpleScript(sc); !simple || argv[0] != fs.bin {
			res.Warn("BUILD_SCRIPT_DIFFERS", fmt.Sprintf("scripts.build is %q; ZEROSETUP runs `%s` directly so the result is predictable.", sc, strings.Join(build[0:], " ")), "package.json")
		}
	}
	start := append([]string{"node_modules/.bin/" + fs.bin}, fs.prodStart...)
	start = append(start, fs.bindArgs(port)...)
	res.Start = ps.Command{Argv: start}
	res.Port = port
	res.Decide("start", strings.Join(start, " "), ps.KindInferred, ps.CertaintyHigh, "production mode: serve the built output with "+fs.bin+" "+strings.Join(fs.prodStart, " "))
	res.Decide("port", strconv.Itoa(port), kind, ps.CertaintyHigh, reason)
}
