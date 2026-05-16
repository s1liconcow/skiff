package aws

import (
	"errors"
	"strings"

	"github.com/s1liconcow/skiff/internal/provider"
)

func ClassifyError(op string, err error) error {
	if err == nil {
		return nil
	}
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return err
	}

	code := provider.CodeProvider
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "accessdenied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "unauthorizedoperation") ||
		strings.Contains(lower, "not authorized") ||
		strings.Contains(lower, "permission"):
		code = provider.CodeAccessDenied
	case strings.Contains(lower, "notfound") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no log streams") ||
		strings.Contains(lower, "no such") ||
		strings.Contains(lower, "does not exist"):
		code = provider.CodeNotFound
	case strings.Contains(lower, "throttl") ||
		strings.Contains(lower, "rate exceeded") ||
		strings.Contains(lower, "too many requests"):
		code = provider.CodeThrottled
	case strings.Contains(lower, "validation") ||
		strings.Contains(lower, "invalidparameter") ||
		strings.Contains(lower, "invalid parameter"):
		code = provider.CodeValidation
	}

	return &provider.Error{
		Code:     code,
		Provider: Name,
		Op:       op,
		Summary:  err.Error(),
		Cause:    err,
	}
}
