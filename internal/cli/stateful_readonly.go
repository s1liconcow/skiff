package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func statefulReadOnlyPlan(providerName string, graph *ir.Graph) *provider.Plan {
	if graph == nil {
		return &provider.Plan{Provider: providerName}
	}
	changes := statefulReadOnlyChanges(graph)
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind == changes[j].Kind {
			return changes[i].LogicalID < changes[j].LogicalID
		}
		return changes[i].Kind < changes[j].Kind
	})
	return &provider.Plan{
		Provider:  providerName,
		Service:   graph.Service,
		Env:       graph.Env,
		Resources: changes,
	}
}

func statefulReadOnlyChanges(graph *ir.Graph) []provider.PlannedChange {
	var changes []provider.PlannedChange
	for _, resource := range graph.Resources.StatefulGroups {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, "StatefulGroup IR root; provider lifecycle apply is gated until stateful lifecycle sagas are implemented"))
	}
	for _, resource := range graph.Resources.StatefulMembers {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, fmt.Sprintf("stateful VM member ordinal %d with stable identity and durable volume", resource.Ordinal)))
	}
	for _, resource := range graph.Resources.StatefulVolumes {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, fmt.Sprintf("durable encrypted volume for member ordinal %d mounted at %s", resource.MemberOrdinal, resource.MountPath)))
	}
	for _, resource := range graph.Resources.StatefulDNS {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, fmt.Sprintf("stable DNS identity for member ordinal %d", resource.MemberOrdinal)))
	}
	for _, resource := range graph.Resources.StatefulRecipes {
		recipe := firstNonEmptyString(resource.Name, resource.Ref)
		if recipe == "" {
			recipe = "stateful recipe"
		}
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, "recipe runtime inputs for "+recipe))
	}
	for _, resource := range graph.Resources.SnapshotPolicies {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, "snapshot backup policy compiled from recipe config"))
	}
	for _, resource := range graph.Resources.UpdatePolicies {
		changes = append(changes, statefulReadOnlyChange(resource.Meta, resource, "ordered StatefulGroup update policy"))
	}
	return changes
}

func statefulReadOnlyChange(meta ir.ResourceMeta, desired any, summary string) provider.PlannedChange {
	body, fingerprint := statefulDesiredBody(desired)
	return provider.PlannedChange{
		Action:      provider.ActionReadOnly,
		Kind:        meta.Kind,
		LogicalID:   meta.LogicalID,
		Name:        meta.Name,
		Tags:        cloneStringMapCLI(meta.Tags),
		Summary:     summary,
		Fingerprint: fingerprint,
		Desired:     body,
	}
}

func statefulDesiredBody(desired any) ([]byte, string) {
	body, err := canonical.Marshal(desired)
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(body)
	return body, "sha256:" + hex.EncodeToString(sum[:])
}

func writeStatefulDeployUnsupported(binary, format, traceID, filePath string, stdout, stderr io.Writer) int {
	summary := "StatefulGroup deploy is not supported until stateful object-state apply and sagas are implemented"
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    "STATEFUL_DEPLOY_UNSUPPORTED",
			Summary: summary,
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "plan_stateful", Command: commandWithOptionalTrace(binary+" plan "+filePath+" --format json", traceID), Mutating: false},
				{ID: "explain_stateful", Command: commandWithOptionalTrace(binary+" explain "+filePath+" --format json", traceID), Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s deploy: %s\n", binary, summary)
	fmt.Fprintf(stderr, "next: %s\n", commandWithOptionalTrace(binary+" plan "+filePath+" --format json", traceID))
	return ExitUserError
}

func commandWithOptionalTrace(command, traceID string) string {
	if traceID == "" {
		return command
	}
	return command + " --trace-id " + traceID
}

func cloneStringMapCLI(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
