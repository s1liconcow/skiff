package e2e_test

type capabilityCoverage struct {
	Capability   string
	Local        string
	AppleSilicon string
	AWS          string
	Evidence     string
	Command      string
}

const (
	coverageCovered        = "covered"
	coverageOptional       = "optional"
	coverageGated          = "gated"
	coverageNotImplemented = "not_implemented"
	coverageNotApplicable  = "not_applicable"
)

func e2eCoverageMatrix() []capabilityCoverage {
	return []capabilityCoverage{
		{
			Capability:   "validate",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageNotApplicable,
			Evidence:     "TestLocalCLIEndToEndCapabilityMatrix runs skiff validate against the http-hello fixture.",
			Command:      "go test ./tests/e2e -run TestLocalCLIEndToEndCapabilityMatrix",
		},
		{
			Capability:   "compile",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageNotApplicable,
			Evidence:     "TestLocalCLIEndToEndCapabilityMatrix runs skiff compile and asserts a service graph.",
			Command:      "go test ./tests/e2e -run TestLocalCLIEndToEndCapabilityMatrix",
		},
		{
			Capability:   "plan",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e uses AWS lowering without credentials; AWS smoke repeats the plan gate when enabled.",
			Command:      "SKIFF_AWS_E2E=1 make e2e-aws",
		},
		{
			Capability:   "explain",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local and AWS smoke explain visible cloud primitives from the fixture service.",
			Command:      "go test ./tests/e2e -run TestLocalCLIEndToEndCapabilityMatrix",
		},
		{
			Capability:   "release signing and verification",
			Local:        coverageCovered,
			AppleSilicon: coverageCovered,
			AWS:          coverageGated,
			Evidence:     "Local e2e verifies release/runtime manifests; Apple publishes signed runtime manifests to RustFS and verifies the fetched release through the CLI; AWS live apply publishes signed release/runtime objects before provider mutation.",
			Command:      "go test ./tests/e2e",
		},
		{
			Capability:   "deploy",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e deploys twice with the fake provider and file object state; AWS live apply deploys core service primitives when live gates are present.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "operation watch",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e starts rollout operations and watches durable operation events; Apple rolls to a second release.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "status",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e reads direct-mode status from file object state; Apple reads direct and local skiffd API status from RustFS S3 object state.",
			Command:      "make e2e-apple-container",
		},
		{
			Capability:   "events",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e checks service events and report object paths; Apple writes runner, operation, and saga events to RustFS and replays canary saga events through local skiffd.",
			Command:      "make e2e-apple-container",
		},
		{
			Capability:   "logs",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e queries fake-provider logs through the real CLI provider factory.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "metrics",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e queries fake-provider metrics through the real CLI provider factory.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "doctor",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e runs direct-mode doctor and rejects critical findings; Apple runs direct and local skiffd API doctor against RustFS-backed status, resource, and event objects.",
			Command:      "make e2e-apple-container",
		},
		{
			Capability:   "canary",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e creates and runs a one-stage canary saga; Apple starts a three-stage rolling canary in direct mode and monitors it through local skiffd.",
			Command:      "make e2e-apple-container",
		},
		{
			Capability:   "rollback",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e rolls back to the previous stable release and verifies service control.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "direct mode",
			Local:        coverageCovered,
			AppleSilicon: coverageCovered,
			AWS:          coverageGated,
			Evidence:     "Local e2e uses --direct with file object state; Apple uses direct mode against RustFS for runner recovery checks and to start the canary saga.",
			Command:      "go test ./tests/e2e",
		},
		{
			Capability:   "drift",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e runs drift against fake provider resource records persisted in object state.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "debug collect",
			Local:        coverageCovered,
			AppleSilicon: coverageNotImplemented,
			AWS:          coverageGated,
			Evidence:     "Local e2e runs skiff debug collect in direct mode against file object state and the fake provider.",
			Command:      "go test ./tests/e2e -run TestLocalCLIEndToEndCapabilityMatrix",
		},
		{
			Capability:   "cost advisor",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e runs skiff cost explain with supplied metrics; AWS remains gated until provider metrics and pricing adapters exist.",
			Command:      "go test ./tests/e2e -run TestLocalCLIEndToEndCapabilityMatrix",
		},
		{
			Capability:   "provider conformance",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "CI and make e2e-local run the provider conformance entry point for implemented providers.",
			Command:      "go test ./tests/conformance/provider",
		},
		{
			Capability:   "plugin conformance",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageNotApplicable,
			Evidence:     "Local e2e runs plugin validate; CI runs the plugin conformance suite.",
			Command:      "go test ./tests/conformance/plugin",
		},
		{
			Capability:   "runner signed release",
			Local:        coverageCovered,
			AppleSilicon: coverageCovered,
			AWS:          coverageGated,
			Evidence:     "Runner fixture serves a signed release; Apple runs signed OCI releases in local Linux VMs.",
			Command:      "go test ./tests/e2e -run TestRunnerServesSignedReleaseFixture",
		},
	}
}
