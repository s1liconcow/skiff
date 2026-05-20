package packages

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Cause   error  `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Summary, e.Cause)
	}
	return e.Summary
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func packageError(code, summary string, cause error) error {
	return &Error{Code: code, Summary: summary, Cause: cause}
}
