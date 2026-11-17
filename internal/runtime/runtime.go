// Package runtime defines the container backend abstraction. The Docker
// implementation lives in runtime/docker; a VM-isolated backend can
// implement the same interface later.
//
// Hardening is the backend's responsibility, not the caller's: a backend
// MUST apply the hardening described in docs/THREAT_MODEL.md to every
// container regardless of the spec, and MUST reject specs it cannot run
// safely.
package runtime

import (
	"context"
	"io"
	"time"
)

// Label keys applied to every ZEROSETUP-owned resource.
const (
	LabelManaged = "io.zerosetup.managed"
	LabelSession = "io.zerosetup.session"
	LabelRole    = "io.zerosetup.role" // app | step | prep | workspace | network | cache
	LabelStep    = "io.zerosetup.step"
)

// Info describes the container host.
type Info struct {
	Backend       string   `json:"backend"`
	Endpoint      string   `json:"endpoint"`
	EndpointFrom  string   `json:"endpointFrom"`
	ServerVersion string   `json:"serverVersion"`
	APIVersion    string   `json:"apiVersion"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	CgroupVersion string   `json:"cgroupVersion"`
	Rootless      bool     `json:"rootless"`
	MemoryLimit   bool     `json:"memoryLimit"`
	PidsLimit     bool     `json:"pidsLimit"`
	CPUs          int      `json:"cpus"`
	MemTotalBytes int64    `json:"memTotalBytes"`
	SecurityOpts  []string `json:"securityOptions"`
}

// ImageInfo identifies a pulled image.
type ImageInfo struct {
	Reference string `json:"reference"`
	ID        string `json:"id"`
	Digest    string `json:"digest"` // repo digest, e.g. node@sha256:...
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Pulled    bool   `json:"pulled"` // false = already present locally
}

// VolumeMount mounts a named volume. Host bind mounts do not exist in this API.
type VolumeMount struct {
	Volume string
	Target string
}

// ContainerSpec describes a container to create.
type ContainerSpec struct {
	Name      string
	Image     string
	Cmd       []string
	Workdir   string
	Env       []string
	User      string // "uid:gid"
	Mounts    []VolumeMount
	Network   string
	Ports     map[int]int // container port -> host port, published on 127.0.0.1
	CPUs      float64
	MemoryMB  int64
	PidsLimit int64
	LogMaxMB  int
	Labels    map[string]string
	// Aliases are extra DNS names on Network (used for managed services).
	Aliases []string
	// Tmpfs are additional writable tmpfs mounts (the root filesystem is read-only).
	Tmpfs []string
	// KeepEntrypoint runs the image's own entrypoint (official service
	// images need it to initialise). Never set for repository code.
	KeepEntrypoint bool
	// PrepChown grants the minimal capabilities needed for the one-shot
	// root "prep" container that only runs chown. Never set for repository code.
	PrepChown bool
}

// ContainerState is the observed state of a container.
type ContainerState struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"` // created|running|exited|dead|...
	Running    bool              `json:"running"`
	ExitCode   int               `json:"exitCode"`
	OOMKilled  bool              `json:"oomKilled"`
	Error      string            `json:"error,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	HostPorts  map[int]int       `json:"hostPorts"`
	Labels     map[string]string `json:"labels"`
}

// Owned lists resources carrying ZEROSETUP labels.
type Owned struct {
	Containers []ContainerState `json:"containers"`
	Volumes    []string         `json:"volumes"`
	Networks   []string         `json:"networks"`
}

// Empty reports whether nothing is owned.
func (o *Owned) Empty() bool {
	return len(o.Containers) == 0 && len(o.Volumes) == 0 && len(o.Networks) == 0
}

// Stats is a point-in-time resource sample.
type Stats struct {
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes uint64    `json:"memoryBytes"`
	MemoryLimit uint64    `json:"memoryLimit"`
	Pids        uint64    `json:"pids"`
	NetRxBytes  uint64    `json:"netRxBytes"`
	NetTxBytes  uint64    `json:"netTxBytes"`
	SampledAt   time.Time `json:"sampledAt"`
}

// Backend is a container execution backend.
type Backend interface {
	Name() string
	Check(ctx context.Context) (*Info, error)
	EnsureImage(ctx context.Context, ref string, progress func(string)) (*ImageInfo, error)
	EnsureNetwork(ctx context.Context, name string, labels map[string]string) error
	EnsureVolume(ctx context.Context, name string, labels map[string]string) error
	Create(ctx context.Context, spec ContainerSpec) (string, error)
	CopyTo(ctx context.Context, id, dest string, tar io.Reader) error
	Start(ctx context.Context, id string) error
	// Wait blocks until the container exits and returns its exit code.
	Wait(ctx context.Context, id string) (int, error)
	Logs(ctx context.Context, id string, follow bool, tail string, stdout, stderr io.Writer) error
	Inspect(ctx context.Context, id string) (*ContainerState, error)
	// Exec runs argv in a running container as the container's user and
	// returns combined stdout. Used for read-only probes.
	Exec(ctx context.Context, id string, argv []string) ([]byte, int, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Remove(ctx context.Context, id string) error
	RemoveVolume(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error
	// ListOwned returns resources whose labels match all of filter.
	ListOwned(ctx context.Context, filter map[string]string) (*Owned, error)
	Stats(ctx context.Context, id string) (*Stats, error)
	Close() error
}

// IsNotFound is implemented by backends to classify missing resources.
type NotFoundError interface{ NotFound() bool }
