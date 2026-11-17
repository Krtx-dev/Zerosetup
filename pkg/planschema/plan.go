// Package planschema defines the versioned ZEROSETUP execution plan.
//
// A Plan is the complete, explicit description of how a repository will be
// run. It is produced by the planner from deterministic detection results,
// validated by the policy package, approved by the user (by hash), and
// executed by the engine. It contains no timestamps and no secret values so
// that identical inputs produce an identical hash.
package planschema

// SchemaVersion is the current plan schema version. Bump the minor version
// for backwards-compatible additions and the major version for breaking
// changes; add a migration in migrate.go for every bump.
const SchemaVersion = "1.1"

// SupportedVersions are the schema versions this build can decode and run.
// 1.1 added Services (additive).
var SupportedVersions = map[string]bool{"1.0": true, "1.1": true}

// Kind describes where a plan decision came from. The ordering below is also
// the precedence order (lower wins).
type Kind string

const (
	KindOverride    Kind = "USER_OVERRIDE" // explicit ZEROSETUP override
	KindDetected    Kind = "DETECTED"      // read from a declaration in the repository
	KindInferred    Kind = "INFERRED"      // derived from conventions/framework knowledge
	KindDefault     Kind = "DEFAULT"       // conservative documented default
	KindUnsupported Kind = "UNSUPPORTED"   // present in the repo but not supported
)

// Certainty is a coarse confidence category. We deliberately avoid fake
// numeric precision.
type Certainty string

const (
	CertaintyDeclared Certainty = "declared" // stated explicitly by the repository
	CertaintyHigh     Certainty = "high"     // strong conventional signal
	CertaintyMedium   Certainty = "medium"   // heuristic match
	CertaintyLow      Certainty = "low"      // weak guess; surfaced as a warning
)

// Evidence is a single observation supporting a decision.
type Evidence struct {
	File   string `json:"file,omitempty"`   // repo-relative path
	Detail string `json:"detail"`           // human-readable explanation
	Signal string `json:"signal,omitempty"` // short machine-readable signal id
}

// Decision records one explainable plan choice.
type Decision struct {
	Field     string     `json:"field"`
	Value     string     `json:"value"`
	Kind      Kind       `json:"kind"`
	Certainty Certainty  `json:"certainty"`
	Reason    string     `json:"reason"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

// Warning is a non-fatal concern the user should see before approving.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
}

// Conflict records contradictory signals that were NOT silently resolved.
type Conflict struct {
	Field      string   `json:"field"`
	Signals    []string `json:"signals"`
	Resolution string   `json:"resolution"`
}

// Source identifies the exact input that was inspected.
type Source struct {
	Kind       string `json:"kind"`                 // "github" | "local"
	Repository string `json:"repository,omitempty"` // e.g. github.com/owner/repo
	URL        string `json:"url,omitempty"`        // canonical clone URL (no credentials)
	Ref        string `json:"ref,omitempty"`        // user-requested ref, if any
	Commit     string `json:"commit,omitempty"`     // resolved commit SHA
	Subdir     string `json:"subdir,omitempty"`
	// ContentHash is a SHA-256 over manifest files; used for local sources
	// without a commit and for cache keys.
	ContentHash string `json:"contentHash"`
}

// Stack is the detected technology stack.
type Stack struct {
	Language              string `json:"language"`
	Framework             string `json:"framework"`
	Runtime               string `json:"runtime"`
	RuntimeVersion        string `json:"runtimeVersion"`
	PackageManager        string `json:"packageManager"`
	PackageManagerVersion string `json:"packageManagerVersion,omitempty"`
}

// Image is the base runtime image.
type Image struct {
	Reference string `json:"reference"` // e.g. docker.io/library/node:24-bookworm-slim
}

// Command is an argv executed directly (never through a shell).
type Command struct {
	Argv []string `json:"argv"`
	// Workdir is relative to the source root inside the workspace.
	Workdir string `json:"workdir,omitempty"`
}

// Stage names.
const (
	StageInstall = "install"
	StageBuild   = "build"
	StageStart   = "start"
)

// Step is a one-shot command run before the application starts.
type Step struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Stage        string  `json:"stage"` // install | build
	Command      Command `json:"command"`
	NeedsNetwork bool    `json:"needsNetwork"`
	TimeoutSec   int     `json:"timeoutSec"`
	Reason       string  `json:"reason"`
}

// Port is a container port the application is expected to listen on.
type Port struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"` // "http"
}

// EnvVar is an environment variable. Secret values are never stored in a
// plan; only the name and the fact that a value must be supplied.
type EnvVar struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Kind     Kind   `json:"kind"`
	Reason   string `json:"reason,omitempty"`
}

// ReadinessCheck describes how the engine decides the app is ready.
type ReadinessCheck struct {
	Type         string `json:"type"` // "http"
	Port         int    `json:"port"` // container port
	Path         string `json:"path,omitempty"`
	ExpectStatus []int  `json:"expectStatus,omitempty"` // empty = any 2xx/3xx is healthy
	BodyContains string `json:"bodyContains,omitempty"`
	TimeoutSec   int    `json:"timeoutSec"`
}

// Resources are enforced container limits.
type Resources struct {
	CPUs            float64 `json:"cpus"`
	MemoryMB        int64   `json:"memoryMB"`
	PidsLimit       int64   `json:"pidsLimit"`
	LogMaxSizeMB    int     `json:"logMaxSizeMB"`
	StartTimeoutSec int     `json:"startTimeoutSec"`
}

// NetworkPolicy documents network exposure. It is descriptive of what is
// enforced; it never claims isolation that is not enforced.
type NetworkPolicy struct {
	PublishHost      string `json:"publishHost"`      // always 127.0.0.1
	InstallOutbound  bool   `json:"installOutbound"`  // install steps have internet access
	RuntimeOutbound  bool   `json:"runtimeOutbound"`  // the running app has internet access
	OutboundFiltered bool   `json:"outboundFiltered"` // false: no egress filtering is enforced
}

// FilesystemPolicy documents filesystem exposure.
type FilesystemPolicy struct {
	HostMounts     bool `json:"hostMounts"` // always false
	ReadonlyRootfs bool `json:"readonlyRootfs"`
}

// ExistingConfig records an environment specification found in the repo.
type ExistingConfig struct {
	Kind     string   `json:"kind"` // dockerfile | compose | devcontainer
	File     string   `json:"file"`
	Used     bool     `json:"used"`
	Reason   string   `json:"reason"`
	Findings []string `json:"findings,omitempty"`
}

// Service is a managed backing service started on the session's private
// network. Services run only when explicitly requested (--with-service).
type Service struct {
	Kind         string   `json:"kind"`  // postgres | redis
	Image        string   `json:"image"` // official image, allowlisted by policy
	Alias        string   `json:"alias"` // DNS name on the session network
	Port         int      `json:"port"`  // container port (never published to the host)
	User         string   `json:"user"`  // non-root uid:gid
	DataDir      string   `json:"dataDir"`
	Tmpfs        []string `json:"tmpfs,omitempty"`
	Command      []string `json:"command,omitempty"`
	Env          []EnvVar `json:"env,omitempty"`
	ReadyCommand []string `json:"readyCommand"`
	Reason       string   `json:"reason"`
}

// Compatibility summarises whether ZEROSETUP can run this plan.
type Compatibility struct {
	Runnable bool     `json:"runnable"`
	Level    string   `json:"level"` // supported | experimental | unsupported
	Reasons  []string `json:"reasons,omitempty"`
}

// Plan is the versioned execution plan.
type Plan struct {
	SchemaVersion   string           `json:"schemaVersion"`
	Source          Source           `json:"source"`
	Stack           Stack            `json:"stack"`
	Image           Image            `json:"image"`
	Steps           []Step           `json:"steps"`
	Start           Command          `json:"start"`
	Ports           []Port           `json:"ports"`
	Env             []EnvVar         `json:"env"`
	Readiness       []ReadinessCheck `json:"readiness"`
	Resources       Resources        `json:"resources"`
	Network         NetworkPolicy    `json:"network"`
	Filesystem      FilesystemPolicy `json:"filesystem"`
	Decisions       []Decision       `json:"decisions"`
	Warnings        []Warning        `json:"warnings"`
	Conflicts       []Conflict       `json:"conflicts"`
	ExistingConfigs []ExistingConfig `json:"existingConfigs"`
	Compatibility   Compatibility    `json:"compatibility"`
	Overrides       []string         `json:"overrides"` // applied override keys, for provenance
	Services        []Service        `json:"services,omitempty"`
}

// PrimaryPort returns the first declared port, or 0.
func (p *Plan) PrimaryPort() int {
	if len(p.Ports) == 0 {
		return 0
	}
	return p.Ports[0].ContainerPort
}
