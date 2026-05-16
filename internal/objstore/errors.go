package objstore

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound           = errors.New("objstore: not found")
	ErrAlreadyExists      = errors.New("objstore: already exists")
	ErrPreconditionFailed = errors.New("objstore: precondition failed")
	ErrConflict           = errors.New("objstore: conflict")
	ErrPermissionDenied   = errors.New("objstore: permission denied")
)

type Error struct {
	Op  string
	Key string
	Err error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Key == "" {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("%s %q: %v", e.Op, e.Key, e.Err)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WrapError(op, key string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Op: op, Key: key, Err: err}
}
