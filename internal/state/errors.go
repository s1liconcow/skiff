package state

import "fmt"

type ErrorCode string

const (
	CodeLeaseHeld          ErrorCode = "LEASE_HELD"
	CodeLeaseLost          ErrorCode = "LEASE_LOST"
	CodePreconditionFailed ErrorCode = "PRECONDITION_FAILED"
	CodeInvalidTransition  ErrorCode = "INVALID_TRANSITION"
)

type sentinelError struct {
	code    ErrorCode
	message string
}

func (e *sentinelError) Error() string {
	return e.message
}

var (
	ErrLeaseHeld          = &sentinelError{code: CodeLeaseHeld, message: "state: lease held"}
	ErrLeaseLost          = &sentinelError{code: CodeLeaseLost, message: "state: lease lost"}
	ErrPreconditionFailed = &sentinelError{code: CodePreconditionFailed, message: "state: precondition failed"}
	ErrStaleETag          = ErrPreconditionFailed
	ErrInvalidTransition  = &sentinelError{code: CodeInvalidTransition, message: "state: invalid transition"}
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Service string    `json:"service,omitempty"`
	Key     string    `json:"key,omitempty"`
	Summary string    `json:"summary"`
	Cause   error     `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Key != "" {
		return fmt.Sprintf("%s %q: %s", e.Code, e.Key, e.Summary)
	}
	if e.Service != "" {
		return fmt.Sprintf("%s service %q: %s", e.Code, e.Service, e.Summary)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Summary)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	sentinel, ok := target.(*sentinelError)
	return ok && e != nil && e.Code == sentinel.code
}

func stateError(code ErrorCode, service, key, summary string, cause error) *Error {
	return &Error{
		Code:    code,
		Service: service,
		Key:     key,
		Summary: summary,
		Cause:   cause,
	}
}
