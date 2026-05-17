package errors

import (
	"testing"

	"github.com/s1liconcow/skiff/internal/provider"
)

func TestCanonicalCodeMappings(t *testing.T) {
	tests := []struct {
		name string
		got  Code
		want Code
	}{
		{name: "config validation", got: FromClientCode("CONFIG_INVALID"), want: ValidationFailed},
		{name: "lease held", got: FromClientCode("LEASE_HELD"), want: LeaseHeld},
		{name: "artifact verify", got: FromVerifyCode("RELEASE_VERIFY_FAILED"), want: ArtifactUntrusted},
		{name: "provider access", got: FromProviderCode(provider.CodeAccessDenied, false), want: PolicyDenied},
		{name: "observability access", got: FromProviderCode(provider.CodeAccessDenied, true), want: ObservabilityUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("code = %s, want %s", tt.got, tt.want)
			}
		})
	}
}
