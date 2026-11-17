// Package engine is ZEROSETUP's application layer. The CLI and the HTTP API
// both call this package; neither contains business logic of its own.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zerosetup/zerosetup/internal/detect"
	"github.com/zerosetup/zerosetup/internal/diagnostics"
	"github.com/zerosetup/zerosetup/internal/inspector"
	"github.com/zerosetup/zerosetup/internal/planner"
	"github.com/zerosetup/zerosetup/internal/readiness"
	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/source"
	"github.com/zerosetup/zerosetup/internal/store"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

// Version is set at build time via -ldflags.
var Version = "0.1.0-dev"

// BackendFactory connects to a container backend lazily, so commands that
// do not execute anything (inspect, plan) work without Docker.
type BackendFactory func(ctx context.Context) (runtime.Backend, error)

// Paths are ZEROSETUP's on-disk locations.
type Paths struct {
	Home    string // ~/.zerosetup or $ZEROSETUP_HOME
	DB      string
	Sources string
	Logs    string
}

// DefaultPaths resolves the data directory.
func DefaultPaths() (Paths, error) {
	home := os.Getenv("ZEROSETUP_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = filepath.Join(h, ".zerosetup")
	}
	return PathsAt(home), nil
}

// PathsAt returns the layout rooted at home.
func PathsAt(home string) Paths {
	return Paths{Home: home, DB: filepath.Join(home, "zerosetup.db"), Sources: filepath.Join(home, "sources"), Logs: filepath.Join(home, "sessions")}
}

// EventListener receives events as they are recorded (in-process only).
type EventListener func(sessions.Event)

// Service is the engine.
type Service struct {
	Paths    Paths
	Store    *store.Store
	Resolver *source.Resolver
	Registry *detect.Registry
	backendF BackendFactory

	mu        sync.Mutex
	backend   runtime.Backend
	listeners map[int]EventListener
	nextL     int
	running   map[string]context.CancelFunc // sessions executing in this process
}

// New builds a service.
func New(paths Paths, st *store.Store, bf BackendFactory) *Service {
	return &Service{
		Paths:     paths,
		Store:     st,
		Resolver:  &source.Resolver{CacheDir: paths.Sources},
		Registry:  inspector.DefaultRegistry(),
		backendF:  bf,
		listeners: map[int]EventListener{},
		running:   map[string]context.CancelFunc{},
	}
}

// Backend returns a connected backend, connecting on first use.
func (s *Service) Backend(ctx context.Context) (runtime.Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend != nil {
		return s.backend, nil
	}
	b, err := s.backendF(ctx)
	if err != nil {
		return nil, err
	}
	s.backend = b
	return b, nil
}

// Close releases resources.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend != nil {
		s.backend.Close()
	}
	return s.Store.Close()
}

// Subscribe registers an in-process event listener; call the returned
// function to unsubscribe.
func (s *Service) Subscribe(l EventListener) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextL
	s.nextL++
	s.listeners[id] = l
	return func() {
		s.mu.Lock()
		delete(s.listeners, id)
		s.mu.Unlock()
	}
}

func (s *Service) emit(ctx context.Context, sid, typ, stage, msg string, data map[string]any) {
	e := sessions.Event{SessionID: sid, Type: typ, Stage: stage, Message: msg, Data: data, Time: time.Now().UTC()}
	// Persist with a detached context so cancellation still records events.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.Store.AppendEvent(pctx, &e)
	s.mu.Lock()
	ls := make([]EventListener, 0, len(s.listeners))
	for _, l := range s.listeners {
		ls = append(ls, l)
	}
	s.mu.Unlock()
	for _, l := range ls {
		l(e)
	}
}

// Facts are engine-recorded run facts stored with the session.
type Facts struct {
	ZerosetupVersion string                   `json:"zerosetupVersion"`
	Input            string                   `json:"input"`
	Ref              string                   `json:"ref,omitempty"`
	SourceDir        string                   `json:"sourceDir"`
	Overrides        planner.Overrides        `json:"overrides"`
	PrevOverrides    *planner.Overrides       `json:"prevOverrides,omitempty"`
	Manifests        []inspector.FileHash     `json:"manifests"`
	Image            *runtime.ImageInfo       `json:"image,omitempty"`
	Engine           *runtime.Info            `json:"engine,omitempty"`
	Pack             *PackResult              `json:"pack,omitempty"`
	Steps            []StepRecord             `json:"steps,omitempty"`
	Readiness        *ReadinessRecord         `json:"readiness,omitempty"`
	Diagnostics      []diagnostics.Diagnostic `json:"diagnostics,omitempty"`
	Cache            *CacheRecord             `json:"cache,omitempty"`
	RepairsApplied   []string                 `json:"repairsApplied,omitempty"`
	SecretNames      []string                 `json:"secretNames,omitempty"`
	Services         []ServiceRecord          `json:"services,omitempty"`
}

// StepRecord records one executed step.
type StepRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Stage      string `json:"stage"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	LogFile    string `json:"logFile"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skipReason,omitempty"`
}

// ReadinessRecord records verification results.
type ReadinessRecord struct {
	Path         string               `json:"path"`
	Attempts     int                  `json:"attempts"`
	DurationMs   int64                `json:"durationMs"`
	Listeners    []readiness.Listener `json:"listeners"`
	LastStatus   int                  `json:"lastStatus"`
	LastError    string               `json:"lastError,omitempty"`
	HTTPReady    bool                 `json:"httpReady"`
	Healthy      bool                 `json:"healthy"`
	FailedReason string               `json:"failedReason,omitempty"`
}

// CacheRecord explains cache usage for a run.
type CacheRecord struct {
	Volume        string `json:"volume"`
	VolumeExisted bool   `json:"volumeExisted"`
	DepsKey       string `json:"depsKey"`
	DepsReused    bool   `json:"depsReused"`
	InstallOK     bool   `json:"installOk"`
	Reason        string `json:"reason"`
}

func loadFacts(x *sessions.Session) *Facts {
	f := &Facts{}
	if len(x.Facts) > 0 {
		_ = json.Unmarshal(x.Facts, f)
	}
	return f
}

func saveFacts(x *sessions.Session, f *Facts) {
	b, _ := json.Marshal(f)
	x.Facts = b
}

// FactsOf exposes a session's facts to interfaces.
func FactsOf(x *sessions.Session) *Facts { return loadFacts(x) }

// Get loads a session after validating the ID format.
func (s *Service) Get(ctx context.Context, id string) (*sessions.Session, error) {
	if !sessions.ValidID(id) {
		return nil, zerr.New(zerr.CodeSessionNotFound, "session", fmt.Sprintf("%q is not a valid session ID", id)).
			WithSuggestion("Session IDs look like zs1a2b3c4d. Run `zerosetup ps --all`.")
	}
	return s.Store.Get(ctx, id)
}

func (s *Service) logDir(sid string) string { return filepath.Join(s.Paths.Logs, sid, "logs") }

func (s *Service) transition(ctx context.Context, x *sessions.Session, next sessions.State, msg string) error {
	prev := x.State
	if err := x.Transition(next); err != nil {
		return err
	}
	if err := s.Store.Save(context.WithoutCancel(ctx), x); err != nil {
		return err
	}
	if msg == "" {
		msg = fmt.Sprintf("%s → %s", prev, next)
	}
	s.emit(ctx, x.ID, "state", string(next), msg, map[string]any{"from": string(prev), "to": string(next)})
	return nil
}
