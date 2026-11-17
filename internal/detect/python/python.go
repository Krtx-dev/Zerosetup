// Package python detects Python web applications. FastAPI is supported;
// other frameworks are recognised and reported as unsupported.
package python

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/zerosetup/zerosetup/internal/detect"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// DefaultVersion is used when nothing is declared (ADR-009).
const DefaultVersion = "3.13"

// SupportedVersions have official python:*-slim images.
var SupportedVersions = map[string]bool{"3.9": true, "3.10": true, "3.11": true, "3.12": true, "3.13": true, "3.14": true}

const uvVersion = "0.13.7"

type Detector struct{}

func New() *Detector { return &Detector{} }

func (*Detector) ID() string    { return "python" }
func (*Detector) Priority() int { return 40 }
func (*Detector) Supports(repo detect.Repo) bool {
	return repo.Exists("pyproject.toml") || repo.Exists("requirements.txt") || repo.Exists("Pipfile") || repo.Exists("setup.py")
}

type pyproject struct {
	Project struct {
		Name           string   `toml:"name"`
		RequiresPython string   `toml:"requires-python"`
		Dependencies   []string `toml:"dependencies"`
	} `toml:"project"`
	Tool struct {
		Poetry *struct {
			Dependencies map[string]any `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// requirementRe accepts PEP 508 requirement strings and rejects anything
// that could be interpreted as a pip option when passed as argv.
var requirementRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._ -]+\])?\s*([<>=!~]=?[^;]*)?(;.*)?$`)

func reqName(req string) string {
	req = strings.TrimSpace(req)
	end := 0
	for end < len(req) && (isAlnum(req[end]) || req[end] == '-' || req[end] == '_' || req[end] == '.') {
		end++
	}
	return strings.ToLower(strings.ReplaceAll(req[:end], "_", "-"))
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (d *Detector) Detect(ctx context.Context, repo detect.Repo, hints detect.Hints) (*detect.Result, error) {
	res := &detect.Result{Stack: ps.Stack{Language: "Python", Runtime: "python"}, Score: 10}
	deps := map[string]string{} // normalized name -> source file
	var pp pyproject
	hasPyproject := false
	if b, err := repo.ReadFile("pyproject.toml", 0); err == nil {
		if _, err := toml.Decode(string(b), &pp); err != nil {
			res.Warn("PYPROJECT_INVALID", "pyproject.toml could not be parsed: "+err.Error(), "pyproject.toml")
		} else {
			hasPyproject = true
			for _, r := range pp.Project.Dependencies {
				deps[reqName(r)] = "pyproject.toml"
			}
			if pp.Tool.Poetry != nil {
				for k := range pp.Tool.Poetry.Dependencies {
					deps[strings.ToLower(k)] = "pyproject.toml"
				}
			}
		}
	}
	var reqOptions []string
	if b, err := repo.ReadFile("requirements.txt", 0); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if strings.HasPrefix(l, "-") {
				reqOptions = append(reqOptions, l)
				continue
			}
			deps[reqName(l)] = "requirements.txt"
		}
	}
	res.Decide("language", "Python", ps.KindDetected, ps.CertaintyDeclared, "Python project manifest found",
		ps.Evidence{File: firstExisting(repo, "pyproject.toml", "requirements.txt", "Pipfile", "setup.py"), Detail: "Python manifest"})

	d.detectVersion(repo, &pp, res, hints)
	pm := d.detectPackageManager(repo, hasPyproject, &pp, deps, reqOptions, res)

	switch {
	case deps["fastapi"] != "":
		res.Stack.Framework = "FastAPI"
		res.Score = 35
		res.Decide("framework", "FastAPI", ps.KindDetected, ps.CertaintyDeclared,
			deps["fastapi"]+" declares fastapi as a dependency", ps.Evidence{File: deps["fastapi"], Detail: "fastapi"})
		if pm != "" {
			d.fastapiStart(repo, deps, res, hints)
		}
	case deps["django"] != "":
		res.Stack.Framework = "Django"
		res.Score = 33
		res.Decide("framework", "Django", ps.KindDetected, ps.CertaintyDeclared, deps["django"]+" declares django", ps.Evidence{File: deps["django"], Detail: "django"})
		if pm != "" {
			d.djangoStart(repo, deps, res, hints)
		}
	case deps["flask"] != "":
		res.Stack.Framework = "Flask"
		res.Score = 33
		res.Decide("framework", "Flask", ps.KindDetected, ps.CertaintyDeclared, deps["flask"]+" declares flask", ps.Evidence{File: deps["flask"], Detail: "flask"})
		if pm != "" {
			d.flaskStart(repo, deps, res, hints)
		}
	default:
		res.Stack.Framework = "Python"
		res.Unsupported = append(res.Unsupported, "no supported Python web framework found (supported: FastAPI, Flask, Django)")
		res.Decide("framework", "unknown", ps.KindUnsupported, ps.CertaintyMedium, "no supported framework dependency")
	}
	detect.ReadEnvTemplates(repo, res)
	for name, kind := range map[string]string{"psycopg": "postgres", "psycopg2": "postgres", "psycopg2-binary": "postgres", "asyncpg": "postgres", "redis": "redis"} {
		if f := deps[name]; f != "" {
			res.Services = append(res.Services, detect.ServiceHint{Kind: kind, Evidence: ps.Evidence{File: f, Detail: "depends on " + name}})
		}
	}
	return res, nil
}

func firstExisting(repo detect.Repo, files ...string) string {
	for _, f := range files {
		if repo.Exists(f) {
			return f
		}
	}
	return ""
}

var pyVerRe = regexp.MustCompile(`(\d+)\.(\d+)`)

func (d *Detector) detectVersion(repo detect.Repo, pp *pyproject, res *detect.Result, hints detect.Hints) {
	ver, kind, cert, reason := DefaultVersion, ps.KindDefault, ps.CertaintyLow, "no Python version declared; using the documented default "+DefaultVersion
	var ev []ps.Evidence
	if b, err := repo.ReadFile(".python-version", 1024); err == nil {
		if m := pyVerRe.FindStringSubmatch(string(b)); m != nil {
			ver, kind, cert, reason = m[1]+"."+m[2], ps.KindDetected, ps.CertaintyDeclared, ".python-version declares "+strings.TrimSpace(string(b))
			ev = append(ev, ps.Evidence{File: ".python-version", Detail: strings.TrimSpace(string(b))})
		}
	} else if rp := strings.TrimSpace(pp.Project.RequiresPython); rp != "" {
		if m := pyVerRe.FindStringSubmatch(rp); m != nil {
			v := m[1] + "." + m[2]
			ev = append(ev, ps.Evidence{File: "pyproject.toml", Detail: "requires-python = " + strconv.Quote(rp)})
			if strings.HasPrefix(rp, ">") && !strings.Contains(rp, "<") && versionLE(v, DefaultVersion) {
				ver, kind, cert, reason = DefaultVersion, ps.KindInferred, ps.CertaintyHigh, fmt.Sprintf("requires-python %q allows the default %s", rp, DefaultVersion)
			} else {
				ver, kind, cert, reason = v, ps.KindDetected, ps.CertaintyHigh, fmt.Sprintf("requires-python %q", rp)
			}
		}
	}
	if hints.RuntimeVersion != "" {
		if m := pyVerRe.FindStringSubmatch(hints.RuntimeVersion); m != nil {
			ver, kind, cert, reason = m[1]+"."+m[2], ps.KindOverride, ps.CertaintyDeclared, "set by --runtime-version"
		}
	}
	if !SupportedVersions[ver] {
		res.Unsupported = append(res.Unsupported, "Python "+ver+" is not supported (supported: 3.9–3.14)")
	}
	if kind == ps.KindDefault {
		res.Warn("PYTHON_VERSION_DEFAULT", reason, "")
	}
	res.Stack.RuntimeVersion = ver
	res.Image = "docker.io/library/python:" + ver + "-slim"
	res.Decide("runtimeVersion", ver, kind, cert, reason, ev...)
}

func versionLE(a, b string) bool {
	pa, pb := pyVerRe.FindStringSubmatch(a), pyVerRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return false
	}
	amaj, _ := strconv.Atoi(pa[1])
	amin, _ := strconv.Atoi(pa[2])
	bmaj, _ := strconv.Atoi(pb[1])
	bmin, _ := strconv.Atoi(pb[2])
	return amaj < bmaj || amaj == bmaj && amin <= bmin
}

func (d *Detector) detectPackageManager(repo detect.Repo, hasPyproject bool, pp *pyproject, deps map[string]string, reqOptions []string, res *detect.Result) string {
	venvBin := ps.VenvDir + "/bin/"
	mkVenv := ps.Step{ID: "venv", Name: "Create virtual environment", Stage: ps.StageInstall, TimeoutSec: 120,
		Command: ps.Command{Argv: []string{"python", "-m", "venv", ps.VenvDir}}, Reason: "isolated environment inside the workspace volume"}
	switch {
	case repo.Exists("uv.lock") && hasPyproject:
		res.Stack.PackageManager, res.Stack.PackageManagerVersion = "uv", uvVersion
		res.Experimental = true
		res.Decide("packageManager", "uv", ps.KindDetected, ps.CertaintyHigh, "uv selected because uv.lock exists", ps.Evidence{File: "uv.lock", Detail: "lockfile"})
		res.Steps = append(res.Steps, mkVenv,
			ps.Step{ID: "install-pm", Name: "Install uv", Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 300,
				Command: ps.Command{Argv: []string{venvBin + "pip", "install", "--disable-pip-version-check", "uv==" + uvVersion}}, Reason: "uv installed into the virtual environment"},
			ps.Step{ID: "install-deps", Name: "Install dependencies", Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 900,
				Command: ps.Command{Argv: []string{venvBin + "uv", "sync", "--frozen", "--no-install-project"}}, Reason: "uv.lock present: frozen sync"})
		res.Env = append(res.Env, ps.EnvVar{Name: "UV_PROJECT_ENVIRONMENT", Value: ps.VenvDir, Kind: ps.KindInferred, Reason: "make uv install into the workspace venv"})
		return "uv"
	case repo.Exists("requirements.txt"):
		res.Stack.PackageManager, res.Stack.PackageManagerVersion = "pip", "bundled"
		res.Decide("packageManager", "pip", ps.KindDetected, ps.CertaintyHigh, "pip selected because requirements.txt exists", ps.Evidence{File: "requirements.txt", Detail: "requirements file"})
		if len(reqOptions) > 0 {
			res.Warn("REQUIREMENTS_OPTIONS", "requirements.txt contains pip options ("+strings.Join(reqOptions, ", ")+"); they will be honoured by pip inside the container.", "requirements.txt")
		}
		if repo.Exists("poetry.lock") || repo.Exists("Pipfile.lock") {
			res.Conflicts = append(res.Conflicts, ps.Conflict{Field: "packageManager", Signals: []string{"requirements.txt", firstExisting(repo, "poetry.lock", "Pipfile.lock")},
				Resolution: "requirements.txt is used; the other lockfile is ignored"})
		}
		res.Steps = append(res.Steps, mkVenv, ps.Step{ID: "install-deps", Name: "Install dependencies", Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 900,
			Command: ps.Command{Argv: []string{venvBin + "pip", "install", "--disable-pip-version-check", "-r", "requirements.txt"}}, Reason: "install requirements.txt"})
		return "pip"
	case hasPyproject && len(pp.Project.Dependencies) > 0:
		res.Stack.PackageManager, res.Stack.PackageManagerVersion = "pip", "bundled"
		var safe []string
		for _, r := range pp.Project.Dependencies {
			r = strings.TrimSpace(r)
			if !requirementRe.MatchString(r) {
				res.Unsupported = append(res.Unsupported, fmt.Sprintf("dependency %q in pyproject.toml is not a plain requirement", r))
				return ""
			}
			safe = append(safe, r)
		}
		res.Decide("packageManager", "pip", ps.KindInferred, ps.CertaintyHigh, "pyproject.toml lists [project].dependencies and there is no lockfile; installing them with pip",
			ps.Evidence{File: "pyproject.toml", Detail: fmt.Sprintf("%d dependencies", len(safe))})
		res.Warn("NO_LOCKFILE", "No Python lockfile; dependency versions are resolved at install time.", "pyproject.toml")
		res.Steps = append(res.Steps, mkVenv, ps.Step{ID: "install-deps", Name: "Install dependencies", Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 900,
			Command: ps.Command{Argv: append([]string{venvBin + "pip", "install", "--disable-pip-version-check"}, safe...)},
			Reason:  "install [project].dependencies without building the project itself"})
		return "pip"
	case repo.Exists("poetry.lock"):
		res.Unsupported = append(res.Unsupported, "Poetry projects are not supported yet")
	case repo.Exists("Pipfile"):
		res.Unsupported = append(res.Unsupported, "Pipenv projects are not supported yet")
	default:
		res.Unsupported = append(res.Unsupported, "no installable dependency specification found")
	}
	return ""
}

var fastapiAppRe = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_.]+\s*)?=\s*(?:fastapi\.)?FastAPI\(`)

func (d *Detector) fastapiStart(repo detect.Repo, deps map[string]string, res *detect.Result, hints detect.Hints) {
	candidates := []string{"main.py", "app.py", "server.py", "api.py", "app/main.py", "src/main.py", "src/app/main.py", "api/main.py", "app/app.py", "backend/main.py"}
	var module, attr, file, appDir string
	for _, c := range candidates {
		b, err := repo.ReadFile(c, 512<<10)
		if err != nil {
			continue
		}
		if m := fastapiAppRe.FindSubmatch(b); m != nil {
			file, attr = c, string(m[1])
			rel := c
			if strings.HasPrefix(c, "src/") {
				appDir, rel = "src", strings.TrimPrefix(c, "src/")
			}
			module = strings.ReplaceAll(strings.TrimSuffix(rel, ".py"), "/", ".")
			break
		}
	}
	if module == "" {
		res.Unsupported = append(res.Unsupported, "could not find a FastAPI application object (looked for `app = FastAPI(...)` in "+strings.Join(candidates, ", ")+")")
		res.Decide("start", "", ps.KindUnsupported, ps.CertaintyMedium, "FastAPI app object not found")
		return
	}
	port, kind, cert, reason := 8000, ps.KindInferred, ps.CertaintyHigh, "uvicorn default port"
	if hints.Port > 0 {
		port, kind, cert, reason = hints.Port, ps.KindOverride, ps.CertaintyDeclared, "set by --port"
	}
	_, hasUvicorn := deps["uvicorn"]
	if !hasUvicorn {
		// fastapi[standard] pulls uvicorn in; a bare fastapi does not.
		res.Steps = append(res.Steps, ps.Step{ID: "install-server", Name: "Install ASGI server", Stage: ps.StageInstall, NeedsNetwork: true, TimeoutSec: 300,
			Command: ps.Command{Argv: []string{ps.VenvDir + "/bin/pip", "install", "--disable-pip-version-check", "uvicorn"}},
			Reason:  "uvicorn is not declared; it is needed to serve the FastAPI app (no-op if fastapi[standard] already installed it)"})
		res.Warn("UVICORN_ADDED", "uvicorn is not declared as a dependency; ZEROSETUP installs it into the environment (the repository is not modified).", deps["fastapi"])
	}
	argv := []string{ps.VenvDir + "/bin/python", "-m", "uvicorn", module + ":" + attr, "--host", "0.0.0.0", "--port", strconv.Itoa(port)}
	if appDir != "" {
		argv = append(argv, "--app-dir", appDir)
	}
	res.Start = ps.Command{Argv: argv}
	res.Port = port
	res.Readiness.Path = "/docs"
	res.Decide("start", strings.Join(argv, " "), ps.KindInferred, ps.CertaintyHigh,
		fmt.Sprintf("FastAPI app object %q found in %s", attr, file), ps.Evidence{File: file, Detail: attr + " = FastAPI(...)"})
	res.Decide("port", strconv.Itoa(port), kind, cert, reason)
	res.Decide("readiness", "GET /docs", ps.KindInferred, ps.CertaintyMedium, "FastAPI serves interactive docs at /docs by default; any HTTP response proves the server is up",
		ps.Evidence{File: path.Clean(file), Detail: "FastAPI default docs_url"})
}

var flaskAppRe = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_.]+\s*)?=\s*(?:flask\.)?Flask\(`)

func (d *Detector) pythonPort(def int, hints detect.Hints, res *detect.Result, why string) int {
	if hints.Port > 0 {
		res.Decide("port", strconv.Itoa(hints.Port), ps.KindOverride, ps.CertaintyDeclared, "set by --port")
		return hints.Port
	}
	res.Decide("port", strconv.Itoa(def), ps.KindInferred, ps.CertaintyHigh, why)
	return def
}

func (d *Detector) gunicorn(deps map[string]string, res *detect.Result, target string, port int) bool {
	if deps["gunicorn"] == "" {
		res.Warn("NO_WSGI_SERVER", "Production mode was requested but gunicorn is not declared; the framework's development server is used instead.", deps["flask"]+deps["django"])
		return false
	}
	res.Start = ps.Command{Argv: []string{ps.VenvDir + "/bin/python", "-m", "gunicorn", "--bind", "0.0.0.0:" + strconv.Itoa(port), target}}
	res.Decide("start", strings.Join(res.Start.Argv, " "), ps.KindInferred, ps.CertaintyHigh, "production mode with gunicorn (declared in "+deps["gunicorn"]+")")
	return true
}

func (d *Detector) flaskStart(repo detect.Repo, deps map[string]string, res *detect.Result, hints detect.Hints) {
	var module, attr, file string
	for _, c := range []string{"app.py", "main.py", "wsgi.py", "server.py", "application.py", "app/__init__.py", "src/app.py"} {
		b, err := repo.ReadFile(c, 512<<10)
		if err != nil {
			continue
		}
		if m := flaskAppRe.FindSubmatch(b); m != nil {
			file, attr = c, string(m[1])
			module = strings.ReplaceAll(strings.TrimSuffix(strings.TrimSuffix(c, "/__init__.py"), ".py"), "/", ".")
			break
		}
	}
	if module == "" {
		res.Unsupported = append(res.Unsupported, "could not find a Flask application object (`app = Flask(__name__)`)")
		return
	}
	port := d.pythonPort(5000, hints, res, "Flask development server default port")
	res.Port, res.Readiness.Path = port, "/"
	if hints.Production() && d.gunicorn(deps, res, module+":"+attr, port) {
		return
	}
	res.Start = ps.Command{Argv: []string{ps.VenvDir + "/bin/python", "-m", "flask", "--app", module + ":" + attr, "run", "--host", "0.0.0.0", "--port", strconv.Itoa(port)}}
	res.Decide("start", strings.Join(res.Start.Argv, " "), ps.KindInferred, ps.CertaintyHigh,
		fmt.Sprintf("Flask app object %q found in %s", attr, file), ps.Evidence{File: file, Detail: attr + " = Flask(...)"})
}

var djangoSettingsRe = regexp.MustCompile(`DJANGO_SETTINGS_MODULE["']\s*,\s*["']([A-Za-z_][A-Za-z0-9_.]*)["']`)

func (d *Detector) djangoStart(repo detect.Repo, deps map[string]string, res *detect.Result, hints detect.Hints) {
	b, err := repo.ReadFile("manage.py", 256<<10)
	if err != nil {
		res.Unsupported = append(res.Unsupported, "Django project without manage.py at the root is not supported")
		return
	}
	settings := ""
	if m := djangoSettingsRe.FindSubmatch(b); m != nil {
		settings = string(m[1])
	}
	port := d.pythonPort(8000, hints, res, "Django runserver default port")
	res.Port, res.Readiness.Path = port, "/"
	if hints.Production() && settings != "" {
		pkgName := strings.TrimSuffix(settings, ".settings")
		if repo.Exists(strings.ReplaceAll(pkgName, ".", "/")+"/wsgi.py") && d.gunicorn(deps, res, pkgName+".wsgi", port) {
			return
		}
	}
	res.Start = ps.Command{Argv: []string{ps.VenvDir + "/bin/python", "manage.py", "runserver", "0.0.0.0:" + strconv.Itoa(port)}}
	reason := "manage.py found at the repository root"
	if settings != "" {
		reason += "; settings module " + settings
	}
	res.Decide("start", strings.Join(res.Start.Argv, " "), ps.KindInferred, ps.CertaintyHigh, reason, ps.Evidence{File: "manage.py", Detail: "Django management script"})
	res.Warn("DJANGO_DEV_SERVER", "Django's development server is used. Database migrations are NOT run automatically; run them yourself if the app needs them.", "manage.py")
}
