// Package zerr defines ZEROSETUP's structured, user-facing errors.
//
// Every error a user can see carries a stable code (ZS-<AREA>-<NNN>), the
// pipeline stage it came from, a one-line summary, and a recovery
// suggestion. The underlying cause is preserved for logs and errors.Is/As.
package zerr

import (
	"errors"
	"fmt"
)

// Code is a stable, documented error identifier.
type Code string

const (
	// Source resolution
	CodeSourceInvalid      Code = "ZS-SOURCE-001"
	CodeSourceUnsupported  Code = "ZS-SOURCE-002"
	CodeSourceNotFound     Code = "ZS-SOURCE-003"
	CodeSourceAuthRequired Code = "ZS-SOURCE-004"
	CodeSourceFetch        Code = "ZS-SOURCE-005"
	CodeSourceTooLarge     Code = "ZS-SOURCE-006"
	CodeGitUnavailable     Code = "ZS-SOURCE-007"

	// Inspection / detection / planning
	CodeInspect          Code = "ZS-INSPECT-001"
	CodeUnsupportedStack Code = "ZS-DETECT-001"
	CodePlanInvalid      Code = "ZS-PLAN-001"
	CodePolicyViolation  Code = "ZS-POLICY-001"

	// Runtime / execution
	CodeDockerUnavailable Code = "ZS-RUNTIME-004"
	CodeImage             Code = "ZS-RUNTIME-005"
	CodeStepFailed        Code = "ZS-RUNTIME-006"
	CodeContainer         Code = "ZS-RUNTIME-007"
	CodeCleanup           Code = "ZS-RUNTIME-008"
	CodePortUnavailable   Code = "ZS-RUNTIME-009"
	CodeNotReady          Code = "ZS-READY-001"
	CodeApprovalRequired  Code = "ZS-APPROVAL-001"
	CodeApprovalMismatch  Code = "ZS-APPROVAL-002"
	CodeCancelled         Code = "ZS-CANCELLED-001"

	// Sessions / storage
	CodeSessionNotFound Code = "ZS-SESSION-001"
	CodeIllegalState    Code = "ZS-SESSION-002"
	CodeStore           Code = "ZS-STORE-001"

	CodeInternal Code = "ZS-INTERNAL-001"
)

// Error is a structured ZEROSETUP error.
type Error struct {
	Code       Code   `json:"code"`
	Stage      string `json:"stage,omitempty"`
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
	Cause      error  `json:"-"`
}

func (e *Error) Error() string {
	msg := string(e.Code) + ": " + e.Summary
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Cause }

// CauseText returns the underlying cause as text (for JSON output).
func (e *Error) CauseText() string {
	if e.Cause == nil {
		return ""
	}
	return e.Cause.Error()
}

// New builds an Error.
func New(code Code, stage, summary string) *Error {
	return &Error{Code: code, Stage: stage, Summary: summary}
}

// Wrap builds an Error around cause.
func Wrap(cause error, code Code, stage, summary string) *Error {
	return &Error{Code: code, Stage: stage, Summary: summary, Cause: cause}
}

// WithSuggestion sets the recovery suggestion.
func (e *Error) WithSuggestion(s string) *Error { e.Suggestion = s; return e }

// WithDetail sets additional human-readable context.
func (e *Error) WithDetail(format string, args ...any) *Error {
	e.Detail = fmt.Sprintf(format, args...)
	return e
}

// As extracts a *Error from err's chain.
func As(err error) (*Error, bool) {
	var ze *Error
	if errors.As(err, &ze) {
		return ze, true
	}
	return nil, false
}

// HasCode reports whether err's chain contains a *Error with code.
func HasCode(err error, code Code) bool {
	ze, ok := As(err)
	return ok && ze.Code == code
}
