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
			Evidence:     "Local e2e verifies release/runtime manifests; Apple publishes signed runtime manifests to RustFS.",
			Command:      "go test ./tests/e2e",
		},
		{
			Capability:   "deploy",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e deploys twice with the fake provider and file object state.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "rollout watch",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e starts and watches provider rollout IDs; Apple rolls to a second release.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "status",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e reads direct-mode status from file object state.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "events",
			Local:        coverageCovered,
			AppleSilicon: coverageOptional,
			AWS:          coverageGated,
			Evidence:     "Local e2e checks service events and report object paths.",
			Command:      "make e2e-local",
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
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e runs direct-mode doctor and rejects critical findings.",
			Command:      "make e2e-local",
		},
		{
			Capability:   "canary",
			Local:        coverageCovered,
			AppleSilicon: coverageNotApplicable,
			AWS:          coverageGated,
			Evidence:     "Local e2e creates and runs a one-stage canary saga with fake provider rollouts.",
			Command:      "make e2e-local",
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
			Evidence:     "Local e2e uses --direct with file object state; runner and Apple tests do not require skiffd.",
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
			Local:        coverageNotImplemented,
			AppleSilicon: coverageNotImplemented,
			AWS:          coverageNotImplemented,
			Evidence:     "The debug command surface is not implemented yet; the matrix keeps the gap explicit.",
			Command:      "br show skiff-jss --json",
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
