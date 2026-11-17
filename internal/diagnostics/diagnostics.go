// Package diagnostics classifies execution failures into structured,
// deterministic diagnostics. When no rule matches it says so explicitly
// rather than inventing a cause.
package diagnostics

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zerosetup/zerosetup/internal/readiness"
)

// Severity levels.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
)

// Change is one field change in a repair proposal, expressed as a plan
// override (repairs never edit the repository).
type Change struct {
	Override string `json:"override"` // port | env | memoryMB | readyTimeoutSec | readinessPath
	Key      string `json:"key,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// Repair is a proposed, reviewable modification of the generated plan.
type Repair struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Explanation string   `json:"explanation"`
	Changes     []Change `json:"changes"`
	Impact      string   `json:"impact"`
}

// Diagnostic is a structured finding.
type Diagnostic struct {
	ID                 string   `json:"id"`
	Category           string   `json:"category"`
	Severity           string   `json:"severity"`
	Summary            string   `json:"summary"`
	Evidence           []string `json:"evidence"`
	LikelyCause        string   `json:"likelyCause"`
	ProposedResolution string   `json:"proposedResolution"`
	Confidence         string   `json:"confidence"` // high | medium | low
	Stage              string   `json:"stage"`
	Docs               string   `json:"docs,omitempty"`
	Repair             *Repair  `json:"repair,omitempty"`
}

// Input is everything known about a failure.
type Input struct {
	Stage          string // install | build | start | verify
	StepID         string
	ExitCode       int
	Exited         bool
	OOMKilled      bool
	MemoryMB       int64
	Logs           string // tail of relevant logs (already redacted)
	Listeners      []readiness.Listener
	ExpectedPort   int
	TimedOut       bool
	TimeoutSec     int
	LastHTTPStatus int
	LastHTTPError  string
	PortConflict   bool
	PackageManager string
	ReadinessPath  string
}

type rule struct {
	id, category, stage string
	re                  *regexp.Regexp
	build               func(m []string, in Input) Diagnostic
}

func evidenceLine(logs string, re *regexp.Regexp) string {
	for _, l := range strings.Split(logs, "\n") {
		if re.MatchString(l) {
			l = strings.TrimSpace(l)
			if len(l) > 240 {
				l = l[:240] + "…"
			}
			return "log: " + l
		}
	}
	return ""
}

var logRules = []rule{
	{id: "ZS-DIAG-LOCKFILE", category: "lockfile-mismatch", stage: "install",
		re: regexp.MustCompile(`(?i)(npm ci.*can only install packages when your package\.json and package-lock\.json.*are in sync|ERR_PNPM_OUTDATED_LOCKFILE|ERR_PNPM_FROZEN_LOCKFILE|Your lockfile needs to be updated|lockfile would have been modified)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "The lockfile is out of sync with package.json",
				LikelyCause:        "package.json was changed without regenerating the lockfile, so a frozen install refuses to proceed.",
				ProposedResolution: "Regenerate the lockfile in the repository (e.g. run your package manager's install and commit the lockfile). ZEROSETUP does not modify lockfiles.",
				Confidence:         "high"}
		}},
	{id: "ZS-DIAG-MISSING-SCRIPT", category: "missing-start-script", stage: "",
		re: regexp.MustCompile(`(?i)(npm (ERR!|error) Missing script: "?([\w:.-]+)|ERR_PNPM_NO_SCRIPT|error Command "([\w:.-]+)" not found)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "The package.json script ZEROSETUP tried to run does not exist",
				LikelyCause:        "The start script name differs from the detected one.",
				ProposedResolution: "Re-plan with --script <name> to choose an existing script.", Confidence: "high"}
		}},
	{id: "ZS-DIAG-EADDRINUSE", category: "port-collision", stage: "",
		re: regexp.MustCompile(`(?i)(EADDRINUSE|address already in use)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "The application tried to bind a port that is already in use inside its container",
				LikelyCause:        "Two servers in the same process tree bind the same port (e.g. a dev server and an API server).",
				ProposedResolution: "Check the application's configuration for duplicate listeners.", Confidence: "high"}
		}},
	{id: "ZS-DIAG-MODULE-NOT-FOUND", category: "dependency-missing", stage: "",
		re: regexp.MustCompile(`(?i)(Cannot find module '([^']+)'|ModuleNotFoundError: No module named '([^']+)'|ERR_MODULE_NOT_FOUND)`),
		build: func(m []string, in Input) Diagnostic {
			mod := firstNonEmpty(m[2], m[3])
			return Diagnostic{Severity: SevError, Summary: "A required module could not be found" + suffix(mod),
				LikelyCause:        "The dependency is not declared in the manifest, the install step skipped it, or the entry point path is wrong.",
				ProposedResolution: "Declare the missing dependency in the repository's manifest, or check the start command's entry file.",
				Confidence:         "medium"}
		}},
	{id: "ZS-DIAG-ENV-MISSING", category: "missing-env", stage: "",
		re: regexp.MustCompile(`(?i)(?:missing|required|not set|undefined|must (?:be )?(?:set|provide)).{0,40}\b([A-Z][A-Z0-9]*_[A-Z0-9_]+)\b|KeyError: '([A-Z][A-Z0-9_]+)'|\b([A-Z][A-Z0-9]*_[A-Z0-9_]+)\b.{0,30}(?:is (?:not set|missing|required|undefined))`),
		build: func(m []string, in Input) Diagnostic {
			name := firstNonEmpty(m[1], m[2], m[3])
			d := Diagnostic{Severity: SevError, Summary: "The application appears to require environment variable " + name,
				LikelyCause:        "The log mentions " + name + " as missing or required.",
				ProposedResolution: "Provide it with --env " + name + "=<value> (non-secret) or --secret " + name + " (secret).",
				Confidence:         "medium"}
			return d
		}},
	{id: "ZS-DIAG-DB", category: "database-connection", stage: "",
		re: regexp.MustCompile(`(?i)(ECONNREFUSED.*:(5432|6379|3306|27017)|could not connect to server|connection to server at .* failed|Can't reach database server|Redis connection.*(failed|refused))`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "The application could not reach a database or cache service",
				LikelyCause:        "It expects a backing service (e.g. PostgreSQL or Redis) that is not running. ZEROSETUP does not provision services in this version.",
				ProposedResolution: "Configure the app to run without the service, or point it at an existing service with --env.",
				Confidence:         "medium"}
		}},
	{id: "ZS-DIAG-DISALLOWED-HOST", category: "host-check", stage: "",
		re: regexp.MustCompile(`(?i)(DisallowedHost|Invalid HTTP_HOST header: '([^']+)')`),
		build: func(m []string, in Input) Diagnostic {
			host := m[2]
			if host == "" {
				host = "127.0.0.1"
			}
			return Diagnostic{Severity: SevError, Summary: "Django rejected the request's Host header" + suffix(host),
				LikelyCause:        "ALLOWED_HOSTS does not include the host used to reach the app. ZEROSETUP's readiness check and your browser use 127.0.0.1.",
				ProposedResolution: "Add '127.0.0.1' and 'localhost' to ALLOWED_HOSTS in the project's settings (or read them from an environment variable you can set with --env).",
				Confidence:         "high"}
		}},
	{id: "ZS-DIAG-EACCES", category: "file-permission", stage: "",
		re: regexp.MustCompile(`(?i)(EACCES|EROFS|Permission denied|read-only file system)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "A file permission error occurred",
				LikelyCause:        "The application writes outside the workspace. Containers run as an unprivileged user with a read-only root filesystem; only /workspace, /cache and /tmp are writable.",
				ProposedResolution: "Configure the application to write into its own directory or /tmp. ZEROSETUP will not run it as root.",
				Confidence:         "medium"}
		}},
	{id: "ZS-DIAG-NETWORK", category: "dependency-install", stage: "install",
		re: regexp.MustCompile(`(?i)(ENOTFOUND|EAI_AGAIN|getaddrinfo|Temporary failure in name resolution|Could not resolve host|ETIMEDOUT)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "Dependency installation could not reach the package registry",
				LikelyCause:        "No network access from the container, DNS failure, or a registry outage.",
				ProposedResolution: "Check that the container engine has internet access, then retry.", Confidence: "medium"}
		}},
	{id: "ZS-DIAG-ENGINE", category: "unsupported-version", stage: "install",
		re: regexp.MustCompile(`(?i)(EBADENGINE|Unsupported engine|requires a different Python|Requires-Python|The engine "node" is incompatible)`),
		build: func(m []string, in Input) Diagnostic {
			return Diagnostic{Severity: SevError, Summary: "A dependency does not support the selected runtime version",
				LikelyCause:        "The runtime version (detected or defaulted) is outside a dependency's supported range.",
				ProposedResolution: "Re-plan with --runtime-version <version> matching the project's requirements.", Confidence: "high"}
		}},
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

// Analyze returns diagnostics for a failure, most relevant first.
func Analyze(in Input) []Diagnostic {
	var out []Diagnostic
	stage := in.Stage
	// Structural signals first: they are certain.
	if in.PortConflict {
		out = append(out, Diagnostic{ID: "ZS-DIAG-HOST-PORT", Category: "port-collision", Severity: SevError, Stage: stage,
			Summary: "The chosen host port was taken by another process", Confidence: "high",
			LikelyCause: "Another program bound the port between selection and container start.", ProposedResolution: "Retry; ZEROSETUP picks a new free port."})
	}
	if in.OOMKilled {
		to := in.MemoryMB * 2
		if to > 16384 {
			to = 16384
		}
		out = append(out, Diagnostic{ID: "ZS-DIAG-OOM", Category: "resource-limit", Severity: SevError, Stage: stage, Confidence: "high",
			Summary:            fmt.Sprintf("The process was killed for exceeding the %d MB memory limit", in.MemoryMB),
			Evidence:           []string{"container state: OOMKilled=true"},
			LikelyCause:        "The workload needs more memory than the plan allows.",
			ProposedResolution: fmt.Sprintf("Raise the memory limit to %d MB.", to),
			Repair: &Repair{ID: "raise-memory", Title: "Raise memory limit", Impact: "re-runs install and start with a larger limit",
				Explanation: "The kernel OOM killer stopped the process.",
				Changes:     []Change{{Override: "memoryMB", From: strconv.FormatInt(in.MemoryMB, 10), To: strconv.FormatInt(to, 10)}}}})
	}
	if in.ExpectedPort > 0 && (stage == "verify" || stage == "start") {
		switch readiness.Evaluate(in.Listeners, in.ExpectedPort) {
		case readiness.PortLoopbackOnly:
			out = append(out, Diagnostic{ID: "ZS-DIAG-LOOPBACK", Category: "loopback-bind", Severity: SevError, Stage: stage, Confidence: "high",
				Summary:            fmt.Sprintf("The application listens on port %d only on the container's loopback interface", in.ExpectedPort),
				Evidence:           []string{"listening sockets: " + listenerList(in.Listeners)},
				LikelyCause:        "The server binds 127.0.0.1/localhost, which is unreachable through Docker port publishing.",
				ProposedResolution: "Make the server bind 0.0.0.0 inside the container. ZEROSETUP can set HOST=0.0.0.0, which many servers honour.",
				Repair: &Repair{ID: "bind-all-interfaces", Title: "Set HOST=0.0.0.0", Impact: "restarts the application with HOST=0.0.0.0 (repository unchanged)",
					Explanation: "Many Node.js servers and dev tools read HOST to choose the bind address.",
					Changes:     []Change{{Override: "env", Key: "HOST", From: "(unset)", To: "0.0.0.0"}}}})
		case readiness.PortNotListening:
			if other := otherPort(in.Listeners, in.ExpectedPort); other > 0 {
				out = append(out, Diagnostic{ID: "ZS-DIAG-PORT-MISMATCH", Category: "port-mismatch", Severity: SevError, Stage: stage, Confidence: "high",
					Summary:            fmt.Sprintf("Expected port %d, but the application listens on %d", in.ExpectedPort, other),
					Evidence:           []string{"listening sockets: " + listenerList(in.Listeners)},
					LikelyCause:        "The port was inferred incorrectly, or the app ignores the PORT variable.",
					ProposedResolution: fmt.Sprintf("Use port %d.", other),
					Repair: &Repair{ID: "use-actual-port", Title: fmt.Sprintf("Use port %d", other), Impact: "re-plans with the observed port and re-runs",
						Explanation: "The kernel socket table shows the server listening on a different port.",
						Changes:     []Change{{Override: "port", From: strconv.Itoa(in.ExpectedPort), To: strconv.Itoa(other)}}}})
			} else if in.TimedOut && !in.Exited {
				out = append(out, Diagnostic{ID: "ZS-DIAG-NOT-LISTENING", Category: "not-listening", Severity: SevError, Stage: stage, Confidence: "medium",
					Summary:            fmt.Sprintf("The process is running but never listened on any TCP port within %ds", in.TimeoutSec),
					Evidence:           []string{"listening sockets: none"},
					LikelyCause:        "The start command may not start a server, it may be waiting for input, or it is still compiling.",
					ProposedResolution: "Check the logs. If the app is slow to start, increase the timeout with --ready-timeout.",
					Repair: &Repair{ID: "longer-timeout", Title: "Double the readiness timeout", Impact: "re-runs verification with a longer timeout",
						Explanation: "Some dev servers compile for a long time on first start.",
						Changes:     []Change{{Override: "readyTimeoutSec", From: strconv.Itoa(in.TimeoutSec), To: strconv.Itoa(min(in.TimeoutSec*2, 1800))}}}})
			}
		case readiness.PortReachable:
			if in.TimedOut {
				d := Diagnostic{ID: "ZS-DIAG-HTTP-NOT-READY", Category: "http-not-ready", Severity: SevError, Stage: stage, Confidence: "medium",
					Summary:            fmt.Sprintf("Application listened on port %d but did not return a successful HTTP response within %ds", in.ExpectedPort, in.TimeoutSec),
					LikelyCause:        "The server is up but errors (5xx) or hangs on the readiness path.",
					ProposedResolution: "Check the logs for request errors. If the app has a dedicated health endpoint, re-plan with --ready-path."}
				if in.LastHTTPStatus > 0 {
					d.Evidence = append(d.Evidence, fmt.Sprintf("last response: HTTP %d for GET %s", in.LastHTTPStatus, in.ReadinessPath))
				}
				if in.LastHTTPError != "" {
					d.Evidence = append(d.Evidence, "last error: "+in.LastHTTPError)
				}
				out = append(out, d)
			}
		}
	}
	if in.Exited && in.ExitCode == 127 && (stage == "start" || stage == "verify") {
		out = append(out, Diagnostic{ID: "ZS-DIAG-NOT-FOUND", Category: "missing-executable", Severity: SevError, Stage: stage, Confidence: "high",
			Summary:            "The start command's executable was not found (exit code 127)",
			Evidence:           []string{"container exit code 127"},
			LikelyCause:        "The program was never installed, e.g. the dependency install skipped this package (a workspace that was ignored, or a missing devDependency).",
			ProposedResolution: "Check the install step's log for skipped or ignored packages, and that the binary is declared in package.json."})
	}
	if in.Exited && !in.OOMKilled && (stage == "start" || stage == "verify") {
		out = append(out, Diagnostic{ID: "ZS-DIAG-CRASH", Category: "process-crash", Severity: SevError, Stage: stage, Confidence: "high",
			Summary:            fmt.Sprintf("The application process exited with code %d", in.ExitCode),
			Evidence:           []string{fmt.Sprintf("container exit code %d", in.ExitCode)},
			LikelyCause:        "See related log diagnostics below, if any.",
			ProposedResolution: "Inspect `zerosetup logs` for the error that caused the exit."})
	}
	// Log-pattern rules.
	for _, r := range logRules {
		if r.stage != "" && r.stage != stage {
			continue
		}
		m := r.re.FindStringSubmatch(in.Logs)
		if m == nil {
			continue
		}
		for len(m) < 5 {
			m = append(m, "")
		}
		d := r.build(m, in)
		d.ID, d.Category, d.Stage = r.id, r.category, stage
		if ev := evidenceLine(in.Logs, r.re); ev != "" {
			d.Evidence = append(d.Evidence, ev)
		}
		out = append(out, d)
	}
	if stage == "install" || stage == "build" {
		if len(out) == 0 {
			out = append(out, Diagnostic{ID: "ZS-DIAG-STEP-FAILED", Category: "dependency-install", Severity: SevError, Stage: stage, Confidence: "low",
				Summary:            fmt.Sprintf("Step %q exited with code %d", in.StepID, in.ExitCode),
				Evidence:           []string{fmt.Sprintf("exit code %d", in.ExitCode)},
				LikelyCause:        "ZEROSETUP could not identify a specific cause from the logs.",
				ProposedResolution: "Read the step log (`zerosetup logs <session> --step " + in.StepID + "`)."})
		}
	}
	if len(out) == 0 {
		out = append(out, Diagnostic{ID: "ZS-DIAG-UNKNOWN", Category: "unknown", Severity: SevError, Stage: stage, Confidence: "low",
			Summary:            "ZEROSETUP could not determine the cause of this failure",
			LikelyCause:        "No deterministic rule matched. This is reported honestly rather than guessed.",
			ProposedResolution: "Read the logs; if you identify the cause, consider contributing a diagnostic rule."})
	}
	sort.SliceStable(out, func(i, j int) bool { return confRank(out[i].Confidence) > confRank(out[j].Confidence) })
	return out
}

func confRank(c string) int {
	switch c {
	case "high":
		return 3
	case "medium":
		return 2
	}
	return 1
}

func otherPort(ls []readiness.Listener, expected int) int {
	for _, l := range ls {
		if l.Port != expected && !l.Loopback && l.Port != 9229 { // 9229: Node inspector
			return l.Port
		}
	}
	return 0
}

func listenerList(ls []readiness.Listener) string {
	if len(ls) == 0 {
		return "none"
	}
	s := make([]string, len(ls))
	for i, l := range ls {
		s[i] = l.String()
	}
	return strings.Join(s, ", ")
}
