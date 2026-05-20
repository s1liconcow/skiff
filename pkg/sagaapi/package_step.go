package sagaapi

import "encoding/json"

const PackageStepSchemaVersion = "skiff.package-step/v1alpha1"

type StepPhase string

const (
	StepPhasePlan       StepPhase = "plan"
	StepPhaseRun        StepPhase = "run"
	StepPhaseResume     StepPhase = "resume"
	StepPhaseCompensate StepPhase = "compensate"
	StepPhaseDoctor     StepPhase = "doctor"
)

type StepStatus string

const (
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
	StepStatusWaiting   StepStatus = "waiting"
	StepStatusRunning   StepStatus = "running"
)

type PackageStepCapability struct {
	Kind          string                 `json:"kind"`
	Summary       string                 `json:"summary,omitempty"`
	Params        map[string]ParamSchema `json:"params,omitempty"`
	Result        map[string]ParamSchema `json:"result,omitempty"`
	Risk          Risk                   `json:"risk,omitempty"`
	Reversibility Reversibility          `json:"reversibility,omitempty"`
}

type PackageStepContext struct {
	Target        string `json:"target,omitempty"`
	TargetKind    string `json:"target_kind,omitempty"`
	Service       string `json:"service,omitempty"`
	Env           string `json:"env,omitempty"`
	OperationID   string `json:"operation_id,omitempty"`
	SagaID        string `json:"saga_id,omitempty"`
	StepID        string `json:"step_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	PackageRef    string `json:"package_ref,omitempty"`
	PackageDigest string `json:"package_digest,omitempty"`
}

type PackageStepRequest struct {
	SchemaVersion   string                     `json:"schema_version"`
	Phase           StepPhase                  `json:"phase"`
	Kind            string                     `json:"kind"`
	Context         PackageStepContext         `json:"context"`
	Params          json.RawMessage            `json:"params,omitempty"`
	PreviousResults map[string]json.RawMessage `json:"previous_results,omitempty"`
	Compensating    *PackageStepResult         `json:"compensating,omitempty"`
}

type PackageStepPlanResponse struct {
	Summary       string           `json:"summary,omitempty"`
	Risk          Risk             `json:"risk,omitempty"`
	Reversibility Reversibility    `json:"reversibility,omitempty"`
	Diagnostics   []StepDiagnostic `json:"diagnostics,omitempty"`
}

type PackageStepResultResponse struct {
	Status             StepStatus             `json:"status"`
	Result             json.RawMessage        `json:"result,omitempty"`
	Failure            *StepFailure           `json:"failure,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	Summary            string                 `json:"summary,omitempty"`
	Diagnostics        []StepDiagnostic       `json:"diagnostics,omitempty"`
}

type PackageStepResult struct {
	Status             StepStatus             `json:"status"`
	Result             json.RawMessage        `json:"result,omitempty"`
	Failure            *StepFailure           `json:"failure,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	Summary            string                 `json:"summary,omitempty"`
}

type PackageStepDoctorResponse struct {
	Findings    []PackageStepFinding `json:"findings,omitempty"`
	Diagnostics []StepDiagnostic     `json:"diagnostics,omitempty"`
}

type PackageStepFinding struct {
	Code       string          `json:"code"`
	Severity   string          `json:"severity,omitempty"`
	Summary    string          `json:"summary"`
	Confidence float64         `json:"confidence,omitempty"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`
}

type StepFailure struct {
	Code       string `json:"code"`
	Summary    string `json:"summary"`
	Cause      string `json:"cause,omitempty"`
	Retriable  bool   `json:"retriable,omitempty"`
	RetryAfter string `json:"retry_after,omitempty"`
}

type ProviderOperationRef struct {
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	ObservedAt  string `json:"observed_at,omitempty"`
	Description string `json:"description,omitempty"`
}

type StepDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Field    string `json:"field,omitempty"`
	Summary  string `json:"summary"`
}
