// Package sessions defines the environment session model and its state
// machine. Persistence lives in internal/store.
package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/zerosetup/zerosetup/internal/zerr"
)

// State is a session lifecycle state.
type State string

const (
	Created          State = "CREATED"
	Inspecting       State = "INSPECTING"
	Planned          State = "PLANNED"
	AwaitingApproval State = "AWAITING_APPROVAL"
	Preparing        State = "PREPARING"
	Building         State = "BUILDING"
	Starting         State = "STARTING"
	Verifying        State = "VERIFYING"
	Ready            State = "READY"
	Failed           State = "FAILED"
	Stopping         State = "STOPPING"
	Stopped          State = "STOPPED"
	Removing         State = "REMOVING"
	Removed          State = "REMOVED"
	Cancelled        State = "CANCELLED"
	Unknown          State = "UNKNOWN"
)

// transitions is the complete set of legal state changes.
var transitions = map[State][]State{
	Created:          {Inspecting, Failed, Cancelled},
	Inspecting:       {Planned, Failed, Cancelled},
	Planned:          {AwaitingApproval, Failed, Cancelled, Removing},
	AwaitingApproval: {Preparing, Cancelled, Removing},
	Preparing:        {Building, Failed, Cancelled, Unknown},
	Building:         {Starting, Failed, Cancelled, Unknown},
	Starting:         {Verifying, Failed, Cancelled, Unknown},
	Verifying:        {Ready, Failed, Cancelled, Unknown},
	Ready:            {Stopping, Failed, Unknown, Verifying, Removing},
	Failed:           {Preparing, Stopping, Removing, AwaitingApproval},
	Stopping:         {Stopped, Failed, Unknown},
	Stopped:          {Starting, Removing, Preparing, AwaitingApproval},
	Cancelled:        {Removing, AwaitingApproval},
	Unknown:          {Ready, Stopped, Failed, Stopping, Removing, Verifying},
	Removing:         {Removed, Failed},
	Removed:          {},
}

// CanTransition reports whether from -> to is legal.
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Active reports whether resources may exist and be in use.
func (s State) Active() bool {
	switch s {
	case Preparing, Building, Starting, Verifying, Ready, Stopping:
		return true
	}
	return false
}

// Executing reports whether a runner process should be driving the session.
func (s State) Executing() bool {
	switch s {
	case Preparing, Building, Starting, Verifying:
		return true
	}
	return false
}

// Terminal reports whether no further transitions are possible.
func (s State) Terminal() bool { return s == Removed }

// Milestone is a fine-grained execution fact (distinct from State).
type Milestone string

const (
	MilestoneImageReady     Milestone = "IMAGE_READY"
	MilestoneDepsInstalled  Milestone = "DEPENDENCIES_INSTALLED"
	MilestoneBuildSuccess   Milestone = "BUILD_SUCCESS"
	MilestoneProcessStarted Milestone = "PROCESS_STARTED"
	MilestonePortListening  Milestone = "PORT_LISTENING"
	MilestoneHTTPReady      Milestone = "HTTP_READY"
	MilestoneHealthy        Milestone = "APPLICATION_HEALTHY"
)

// Session is a persisted environment session.
type Session struct {
	ID           string                  `json:"id"`
	State        State                   `json:"state"`
	Source       string                  `json:"source"`     // what the user typed (credential-free)
	Repository   string                  `json:"repository"` // github.com/o/r or local path
	Commit       string                  `json:"commit,omitempty"`
	Framework    string                  `json:"framework"`
	Runtime      string                  `json:"runtime"`
	PlanHash     string                  `json:"planHash"`
	PlanJSON     []byte                  `json:"-"`
	ApprovedAt   *time.Time              `json:"approvedAt,omitempty"`
	URL          string                  `json:"url,omitempty"`
	HostPort     int                     `json:"hostPort,omitempty"`
	ContainerID  string                  `json:"containerId,omitempty"`
	ImageDigest  string                  `json:"imageDigest,omitempty"`
	Milestones   map[Milestone]time.Time `json:"milestones"`
	Error        *zerr.Error             `json:"error,omitempty"`
	RunnerPID    int                     `json:"runnerPid,omitempty"`
	CreatedAt    time.Time               `json:"createdAt"`
	UpdatedAt    time.Time               `json:"updatedAt"`
	Attempt      int                     `json:"attempt"`
	PrevPlanHash string                  `json:"prevPlanHash,omitempty"` // for repair revert
	// Facts holds engine-recorded run facts (image identity, manifest
	// hashes, readiness results, platform). Opaque to this package.
	Facts json.RawMessage `json:"facts,omitempty"`
}

var idRe = regexp.MustCompile(`^zs[0-9a-f]{8}$`)

// NewID returns a fresh session ID like "zs1a2b3c4d".
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "zs" + hex.EncodeToString(b)
}

// ValidID reports whether id has the session ID format.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Transition moves s to next or returns an illegal-state error.
func (s *Session) Transition(next State) error {
	if s.State == next {
		return nil
	}
	if !CanTransition(s.State, next) {
		return zerr.New(zerr.CodeIllegalState, "session", fmt.Sprintf("cannot move session %s from %s to %s", s.ID, s.State, next))
	}
	s.State = next
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// Event is a persisted, ordered session event.
type Event struct {
	Seq       int64          `json:"seq"`
	SessionID string         `json:"sessionId"`
	Time      time.Time      `json:"time"`
	Type      string         `json:"type"` // state | stage | milestone | log | warning | error | progress | diagnostic
	Stage     string         `json:"stage,omitempty"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
}
