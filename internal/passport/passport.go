// Package passport defines the Reproducibility Passport: a versioned,
// secret-free record of how a session ran. See schemas/passport-1.1.json.
package passport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the passport schema version.
const SchemaVersion = "1.1"

// SupportedVersions can be read by this build. 1.1 added
// execution.overridesDetail and services (additive).
var SupportedVersions = map[string]bool{"1.0": true, "1.1": true}

// Passport is the exported record.
type Passport struct {
	SchemaVersion string          `json:"schemaVersion"`
	Kind          string          `json:"kind"` // always "zerosetup.passport"
	Generator     Generator       `json:"generator"`
	Session       SessionInfo     `json:"session"`
	Source        Source          `json:"source"`
	Environment   Environment     `json:"environment"`
	Dependencies  Dependencies    `json:"dependencies"`
	Execution     Execution       `json:"execution"`
	Verification  Verification    `json:"verification"`
	Detection     Detection       `json:"detection"`
	Platform      Platform        `json:"platform"`
	Timings       []Timing        `json:"timings"`
	Diagnostics   []Diagnostic    `json:"diagnostics"`
	Exclusions    []string        `json:"exclusions"`
	Plan          json.RawMessage `json:"plan"`
}

type Generator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SessionInfo struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Attempt    int    `json:"attempt"`
	CreatedAt  string `json:"createdAt"`
	ApprovedAt string `json:"approvedAt,omitempty"`
	ExportedAt string `json:"exportedAt"`
}

type Source struct {
	Kind        string `json:"kind"`
	Repository  string `json:"repository,omitempty"`
	URL         string `json:"url,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Subdir      string `json:"subdir,omitempty"`
	ContentHash string `json:"contentHash"`
}

type Environment struct {
	Runtime               string   `json:"runtime"`
	RuntimeVersion        string   `json:"runtimeVersion"`
	Framework             string   `json:"framework"`
	PackageManager        string   `json:"packageManager"`
	PackageManagerVersion string   `json:"packageManagerVersion,omitempty"`
	BaseImage             string   `json:"baseImage"`
	BaseImageDigest       string   `json:"baseImageDigest,omitempty"`
	BaseImageID           string   `json:"baseImageId,omitempty"`
	ImageOS               string   `json:"imageOs,omitempty"`
	ImageArchitecture     string   `json:"imageArchitecture,omitempty"`
	EnvironmentVariables  []EnvVar `json:"environmentVariables"`
}

type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"` // value intentionally omitted
	Kind   string `json:"kind"`
}

type FileHash struct {
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Lockfile bool   `json:"lockfile"`
}

type Dependencies struct {
	Manifests []FileHash `json:"manifests"`
	Locked    bool       `json:"locked"` // a lockfile pinned versions
}

type Step struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Stage      string   `json:"stage"`
	Argv       []string `json:"argv"`
	ExitCode   *int     `json:"exitCode,omitempty"`
	DurationMs int64    `json:"durationMs,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
}

type Execution struct {
	PlanSchemaVersion string         `json:"planSchemaVersion"`
	PlanHash          string         `json:"planHash"`
	Steps             []Step         `json:"steps"`
	Start             []string       `json:"start"`
	ContainerPort     int            `json:"containerPort"`
	Resources         map[string]any `json:"resources"`
	Security          map[string]any `json:"security"`
	Cache             map[string]any `json:"cache,omitempty"`
	Overrides         []string       `json:"overrides"`
	// OverridesDetail are the exact ZEROSETUP overrides used (no secret
	// values: secrets appear by name only). Replay uses them.
	OverridesDetail json.RawMessage `json:"overridesDetail,omitempty"`
	Services        []ServiceInfo   `json:"services,omitempty"`
	RepairsApplied  []string        `json:"repairsApplied"`
	Status          string          `json:"status"`
}

// ServiceInfo identifies a managed service image.
type ServiceInfo struct {
	Kind   string `json:"kind"`
	Image  string `json:"image"`
	Digest string `json:"digest,omitempty"`
}

type Verification struct {
	Milestones map[string]string `json:"milestones"`
	Readiness  map[string]any    `json:"readiness,omitempty"`
}

type Decision struct {
	Field     string   `json:"field"`
	Value     string   `json:"value"`
	Kind      string   `json:"kind"`
	Certainty string   `json:"certainty"`
	Reason    string   `json:"reason"`
	Evidence  []string `json:"evidence,omitempty"`
}

type Detection struct {
	Decisions []Decision `json:"decisions"`
	Warnings  []string   `json:"warnings"`
	Conflicts []string   `json:"conflicts"`
}

type Platform struct {
	HostOS        string `json:"hostOs"`
	HostArch      string `json:"hostArch"`
	Engine        string `json:"engine,omitempty"`
	EngineVersion string `json:"engineVersion,omitempty"`
	EngineOS      string `json:"engineOs,omitempty"`
	EngineArch    string `json:"engineArch,omitempty"`
	CgroupVersion string `json:"cgroupVersion,omitempty"`
}

type Timing struct {
	Attempt int    `json:"attempt"`
	Name    string `json:"name"`
	Millis  int64  `json:"millis"`
}

type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

// Marshal returns stable, indented JSON.
func Marshal(p *Passport) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var homeRe = regexp.MustCompile(`(/Users/|/home/)[^/"\s]+`)

// Scrub removes personal filesystem paths (home directory names) and any
// known secret values from serialized output, then verifies none remain.
func Scrub(b []byte, secrets []string) ([]byte, error) {
	out := homeRe.ReplaceAll(b, []byte("${1}<user>"))
	for _, s := range secrets {
		if len(s) >= 4 {
			out = bytes.ReplaceAll(out, []byte(s), []byte("[REDACTED]"))
		}
	}
	for _, s := range secrets {
		if len(s) >= 4 && bytes.Contains(out, []byte(s)) {
			return nil, fmt.Errorf("secret value survived scrubbing")
		}
	}
	return out, nil
}

// Validate performs structural checks on a decoded passport.
func Validate(p *Passport) error {
	var probs []string
	if !SupportedVersions[p.SchemaVersion] {
		probs = append(probs, "unsupported schemaVersion "+p.SchemaVersion)
	}
	if p.Kind != "zerosetup.passport" {
		probs = append(probs, "kind must be zerosetup.passport")
	}
	if p.Source.ContentHash == "" {
		probs = append(probs, "source.contentHash required")
	}
	if !strings.HasPrefix(p.Execution.PlanHash, "sha256:") {
		probs = append(probs, "execution.planHash must be sha256:")
	}
	for _, e := range p.Environment.EnvironmentVariables {
		if e.Secret && e.Value != "" {
			probs = append(probs, "secret "+e.Name+" carries a value")
		}
	}
	if len(probs) > 0 {
		return fmt.Errorf("invalid passport: %s", strings.Join(probs, "; "))
	}
	return nil
}
