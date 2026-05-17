package errors

import "github.com/s1liconcow/skiff/internal/provider"

type Code string

const (
	ValidationFailed         Code = "VALIDATION_FAILED"
	PolicyDenied             Code = "POLICY_DENIED"
	ArtifactUntrusted        Code = "ARTIFACT_UNTRUSTED"
	LeaseHeld                Code = "LEASE_HELD"
	CloudApplyFailed         Code = "CLOUD_APPLY_FAILED"
	RolloutFailed            Code = "ROLLOUT_FAILED"
	CanaryFailed             Code = "CANARY_FAILED"
	RollbackFailed           Code = "ROLLBACK_FAILED"
	ObservabilityUnavailable Code = "OBSERVABILITY_UNAVAILABLE"
	InternalError            Code = "INTERNAL_ERROR"
)

func FromClientCode(code string) Code {
	switch code {
	case "", "INTERNAL_ERROR":
		return InternalError
	case "CONFIG_INVALID", "CONFIG_LOAD_FAILED", "CLI_INVALID", "EVENT_SCOPE_INVALID", "STATE_INVALID", "STATE_PATH_INVALID", "UNSUPPORTED_CLIENT_MODE", "API_URL_INVALID", "API_URL_REQUIRED":
		return ValidationFailed
	case "LEASE_HELD":
		return LeaseHeld
	case "POLICY_DENIED":
		return PolicyDenied
	case "ROLLOUT_FAILED":
		return RolloutFailed
	case "ROLLBACK_FAILED":
		return RollbackFailed
	case "ARTIFACT_UNTRUSTED":
		return ArtifactUntrusted
	}
	return Code(code)
}

func FromSpecCode(code string) Code {
	switch code {
	case "POLICY_DENIED":
		return PolicyDenied
	case "DEPLOY_FAILED", "PLAN_FAILED":
		return CloudApplyFailed
	case "ROLLOUT_FAILED":
		return RolloutFailed
	case "CANARY_FAILED":
		return CanaryFailed
	case "SPEC_RENDER_FAILED":
		return InternalError
	default:
		return ValidationFailed
	}
}

func FromVerifyCode(code string) Code {
	switch code {
	case "RELEASE_VERIFY_INVALID", "OBJECT_VERIFY_INVALID":
		return ValidationFailed
	default:
		return ArtifactUntrusted
	}
}

func FromProviderCode(code provider.ErrorCode, observability bool) Code {
	if observability {
		if code == provider.CodeValidation {
			return ValidationFailed
		}
		return ObservabilityUnavailable
	}
	switch code {
	case provider.CodeAccessDenied:
		return PolicyDenied
	case provider.CodeValidation, provider.CodeInvalidConfig, provider.CodeUnsupported:
		return ValidationFailed
	default:
		return CloudApplyFailed
	}
}
