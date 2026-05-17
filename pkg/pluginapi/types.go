// Package pluginapi defines the stable data contract between Skiff and trusted
// plugins. Plugins extend Skiff by returning typed data for the host to validate
// and apply; they do not receive raw cloud clients.
package pluginapi

import "encoding/json"

const (
	APIVersion = "skiff.dev/plugin/v1alpha1"
	KindPlugin = "Plugin"
)

type Hook string

const (
	HookValidate      Hook = "validate"
	HookMutateIR      Hook = "mutate_ir"
	HookRuntimeAddons Hook = "runtime_addons"
	HookDoctorChecks  Hook = "doctor_checks"
	HookSagaStep      Hook = "saga_step"
)

type CapabilityKind string

const (
	CapabilityIRPatch      CapabilityKind = "ir_patch"
	CapabilityRuntimeAddon CapabilityKind = "runtime_addon"
	CapabilityDoctorCheck  CapabilityKind = "doctor_check"
	CapabilitySagaStep     CapabilityKind = "saga_step"
)

type RuntimeKind string

const (
	RuntimeManifestOnly RuntimeKind = "manifest"
	RuntimeCommand      RuntimeKind = "command"
	RuntimeGRPC         RuntimeKind = "grpc"
)

type PatchOp string

const (
	PatchAdd PatchOp = "add"
)

type Manifest struct {
	APIVersion   string       `json:"apiVersion"`
	Kind         string       `json:"kind"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description,omitempty"`
	Runtime      RuntimeSpec  `json:"runtime,omitempty"`
	Hooks        []Hook       `json:"hooks,omitempty"`
	Permissions  Permissions  `json:"permissions,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Package      *PackageRef  `json:"package,omitempty"`
}

type RuntimeSpec struct {
	Kind     RuntimeKind `json:"kind,omitempty"`
	Command  []string    `json:"command,omitempty"`
	Endpoint string      `json:"endpoint,omitempty"`
	Protocol string      `json:"protocol,omitempty"`
}

type PackageRef struct {
	Ref          string `json:"ref"`
	Digest       string `json:"digest,omitempty"`
	SignatureRef string `json:"signature_ref,omitempty"`
}

type Permissions struct {
	AllowedPatchKinds []string `json:"allowed_patch_kinds,omitempty"`
	RuntimeAddons     bool     `json:"runtime_addons,omitempty"`
	DoctorChecks      bool     `json:"doctor_checks,omitempty"`
	SagaStepKinds     []string `json:"saga_step_kinds,omitempty"`
}

type Capability struct {
	Kind          CapabilityKind `json:"kind"`
	Name          string         `json:"name"`
	Version       string         `json:"version,omitempty"`
	Description   string         `json:"description,omitempty"`
	PatchKinds    []string       `json:"patch_kinds,omitempty"`
	RuntimeAddons []string       `json:"runtime_addons,omitempty"`
	DoctorChecks  []string       `json:"doctor_checks,omitempty"`
	SagaStepKinds []string       `json:"saga_step_kinds,omitempty"`
}

type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Field    string `json:"field,omitempty"`
	Summary  string `json:"summary"`
}

type PatchSource struct {
	Plugin     string `json:"plugin,omitempty"`
	Version    string `json:"version,omitempty"`
	Capability string `json:"capability,omitempty"`
}

type IRPatch struct {
	Op      PatchOp         `json:"op"`
	Path    string          `json:"path"`
	Kind    string          `json:"kind"`
	Value   json.RawMessage `json:"value"`
	Summary string          `json:"summary,omitempty"`
	Source  PatchSource     `json:"source,omitempty"`
}

type ValidateRequest struct {
	Manifest Manifest        `json:"manifest"`
	Spec     json.RawMessage `json:"spec,omitempty"`
	TraceID  string          `json:"trace_id,omitempty"`
}

type ValidateResponse struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type MutateIRRequest struct {
	Manifest Manifest        `json:"manifest"`
	Graph    json.RawMessage `json:"graph"`
	Spec     json.RawMessage `json:"spec,omitempty"`
	TraceID  string          `json:"trace_id,omitempty"`
}

type MutateIRResponse struct {
	Patches     []IRPatch    `json:"patches,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type RuntimeAddonsRequest struct {
	Manifest Manifest        `json:"manifest"`
	Graph    json.RawMessage `json:"graph"`
	TraceID  string          `json:"trace_id,omitempty"`
}

type RuntimeAddonsResponse struct {
	Addons      []RuntimeAddon `json:"addons,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
}

type RuntimeAddon struct {
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Target     string          `json:"target,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
	SecretRefs []SecretRef     `json:"secret_refs,omitempty"`
}

type SecretRef struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

type DoctorChecksRequest struct {
	Manifest Manifest        `json:"manifest"`
	Status   json.RawMessage `json:"status"`
	Service  json.RawMessage `json:"service"`
	TraceID  string          `json:"trace_id,omitempty"`
}

type DoctorChecksResponse struct {
	Findings    []DoctorFinding `json:"findings,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
}

type DoctorFinding struct {
	Code       string          `json:"code"`
	Severity   string          `json:"severity,omitempty"`
	Service    string          `json:"service,omitempty"`
	Summary    string          `json:"summary"`
	Confidence float64         `json:"confidence,omitempty"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`
}

type SagaStepRequest struct {
	Manifest        Manifest                   `json:"manifest"`
	Phase           string                     `json:"phase"`
	Kind            string                     `json:"kind"`
	SagaID          string                     `json:"saga_id,omitempty"`
	StepID          string                     `json:"step_id,omitempty"`
	Params          json.RawMessage            `json:"params,omitempty"`
	TraceID         string                     `json:"trace_id,omitempty"`
	PreviousResults map[string]json.RawMessage `json:"previous_results,omitempty"`
}

type SagaStepPlanResponse struct {
	Summary       string       `json:"summary,omitempty"`
	Risk          string       `json:"risk,omitempty"`
	Reversibility string       `json:"reversibility,omitempty"`
	Diagnostics   []Diagnostic `json:"diagnostics,omitempty"`
}

type SagaStepResultResponse struct {
	Status             string                 `json:"status"`
	Result             json.RawMessage        `json:"result,omitempty"`
	Failure            *StepFailure           `json:"failure,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	Summary            string                 `json:"summary,omitempty"`
	Diagnostics        []Diagnostic           `json:"diagnostics,omitempty"`
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
