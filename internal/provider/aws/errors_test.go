package aws_test

import (
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code provider.ErrorCode
	}{
		{name: "access denied", err: errors.New("AccessDenied: user is not authorized"), code: provider.CodeAccessDenied},
		{name: "not found", err: errors.New("TargetGroupNotFound: target group does not exist"), code: provider.CodeNotFound},
		{name: "throttled", err: errors.New("ThrottlingException: rate exceeded"), code: provider.CodeThrottled},
		{name: "validation", err: errors.New("ValidationError: invalid parameter"), code: provider.CodeValidation},
		{name: "provider", err: errors.New("connection reset"), code: provider.CodeProvider},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := aws.ClassifyError("test", tc.err)
			var providerErr *provider.Error
			if !errors.As(err, &providerErr) {
				t.Fatalf("ClassifyError() = %T, want provider.Error", err)
			}
			if providerErr.Code != tc.code {
				t.Fatalf("code = %s, want %s", providerErr.Code, tc.code)
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("classified error does not wrap original error")
			}
		})
	}
}
