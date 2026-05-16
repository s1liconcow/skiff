package s3store

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/s1liconcow/skiff/internal/objstore"
)

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Err        error
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("s3 %s: %s", e.Code, e.Message)
	}
	if e.Code != "" {
		return "s3 " + e.Code
	}
	if e.Message != "" {
		return "s3: " + e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("s3 http status %d", e.StatusCode)
}

func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *APIError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

func (e *APIError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *APIError) ErrorMessage() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type httpStatusCoder interface {
	HTTPStatusCode() int
}

type awsAPIError interface {
	ErrorCode() string
	ErrorMessage() string
}

func classifyError(op, key string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, objstore.ErrNotFound) ||
		errors.Is(err, objstore.ErrAlreadyExists) ||
		errors.Is(err, objstore.ErrPreconditionFailed) ||
		errors.Is(err, objstore.ErrConflict) ||
		errors.Is(err, objstore.ErrPermissionDenied) {
		return objstore.WrapError(op, key, err)
	}

	status := 0
	if statusErr, ok := err.(httpStatusCoder); ok {
		status = statusErr.HTTPStatusCode()
	}

	code := ""
	var apiErr awsAPIError
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode()
	}

	classified := classifyCode(op, status, code)
	if classified == nil {
		classified = err
	}
	return objstore.WrapError(op, key, classified)
}

func classifyCode(op string, status int, code string) error {
	switch code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return objstore.ErrNotFound
	case "PreconditionFailed":
		if op == "create" {
			return objstore.ErrAlreadyExists
		}
		return objstore.ErrPreconditionFailed
	case "ConditionalRequestConflict", "OperationAborted", "Conflict":
		return objstore.ErrConflict
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken":
		return objstore.ErrPermissionDenied
	}

	switch status {
	case http.StatusNotFound:
		return objstore.ErrNotFound
	case http.StatusPreconditionFailed:
		if op == "create" {
			return objstore.ErrAlreadyExists
		}
		return objstore.ErrPreconditionFailed
	case http.StatusConflict:
		return objstore.ErrConflict
	case http.StatusForbidden, http.StatusUnauthorized:
		return objstore.ErrPermissionDenied
	}

	return nil
}
