package policy

import "fmt"

type Explanation struct {
	Role      Role     `json:"role"`
	Sid       string   `json:"sid"`
	Actions   []string `json:"actions"`
	Resources []string `json:"resources"`
	Reason    string   `json:"reason"`
	Safety    string   `json:"safety"`
}

func Explain(role Role, document Document) []Explanation {
	out := make([]Explanation, 0, len(document.Statement))
	for _, statement := range document.Statement {
		out = append(out, Explanation{
			Role:      role,
			Sid:       statement.Sid,
			Actions:   stringValues(statement.Action),
			Resources: stringValues(statement.Resource),
			Reason:    reasonForStatement(role, statement.Sid),
			Safety:    safetyForStatement(statement),
		})
	}
	return out
}

func reasonForStatement(role Role, sid string) string {
	reasons := map[string]string{
		"DenyInsecureTransport":         "Require TLS for every state bucket request.",
		"DenyMissingKMSEncryption":      "Reject object writes that omit server-side KMS encryption.",
		"DenyNonKMSEncryption":          "Reject object writes that request non-KMS server-side encryption.",
		"DenyStateDeletes":              "Keep durable object-state history and controls from being deleted by default.",
		"DenyUnconditionalStateWrites":  "Force create-if-absent or compare-and-swap headers on state writes.",
		"ReadStateBucketLocation":       "Allow clients to discover the bucket region without granting object access.",
		"ListServiceState":              "Allow listing service state prefixes needed for service operations.",
		"ListSagaState":                 "Allow listing saga state prefixes needed for operational workflows.",
		"ListIndexState":                "Allow skiffd to read rebuildable index object prefixes.",
		"ListResourceState":             "Allow skiffd to discover cloud resource record prefixes.",
		"ListAllStateForEmergency":      "Allow emergency operators to enumerate state during break-glass recovery.",
		"ReadServiceControlAndReleases": "Allow skiff-runner to read environment root, service control, release, and runtime manifests.",
		"ReadOperationalState":          "Allow deployer and skiffd flows to inspect operation, saga, release, and audit state.",
		"ReadIndexesAndResources":       "Allow skiffd to serve index and resource views without making them durable truth.",
		"ReadAllStateForEmergency":      "Allow emergency operators to inspect all object-state during recovery.",
		"CreateImmutableState":          "Allow create-only immutable release, intent, event, audit, and resource records.",
		"CASControlState":               "Allow compare-and-swap updates to service, operation, saga, and index controls.",
		"EmergencyCreateOnlyState":      "Allow emergency creation of immutable state objects with create-if-absent protection.",
		"EmergencyCASControlState":      "Allow emergency compare-and-swap updates to control documents.",
		"UseStateKMSKey":                "Allow use of the configured KMS alias for encrypted state object access.",
		"UseReleaseSigningKMSKey":       "Allow release signing with the configured asymmetric KMS key.",
	}
	if reason := reasons[sid]; reason != "" {
		return reason
	}
	return fmt.Sprintf("Permission for %s policy statement %s.", role, sid)
}

func safetyForStatement(statement Statement) string {
	if statement.Effect == "Deny" {
		return "guardrail"
	}
	actions := stringValues(statement.Action)
	if containsAction(actions, "s3:PutObject") {
		if hasConditionalWrite(statement) {
			return "conditional_write"
		}
		return "write"
	}
	if hasActionPrefix(actions, "kms:") {
		return "kms_scoped"
	}
	return "read"
}
