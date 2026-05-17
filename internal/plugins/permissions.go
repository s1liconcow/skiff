package plugins

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

const (
	PatchKindSecurityGroupRule = "SecurityGroupRule"
)

type PermissionError struct {
	Plugin  string `json:"plugin"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

func (e PermissionError) Error() string {
	if e.Summary != "" {
		return e.Summary
	}
	return fmt.Sprintf("plugin %s is not allowed to emit %s patches", e.Plugin, e.Kind)
}

func EnforcePatches(plugin Plugin, patches []pluginapi.IRPatch) ([]pluginapi.IRPatch, error) {
	allowed := stringSet(plugin.Manifest.Permissions.AllowedPatchKinds)
	out := make([]pluginapi.IRPatch, 0, len(patches))
	for i, patch := range patches {
		if patch.Op == "" {
			patch.Op = pluginapi.PatchAdd
		}
		if patch.Op != pluginapi.PatchAdd {
			return nil, fmt.Errorf("plugin %s patch %d uses unsupported op %q", plugin.Manifest.Name, i, patch.Op)
		}
		if strings.TrimSpace(patch.Kind) == "" {
			return nil, fmt.Errorf("plugin %s patch %d is missing kind", plugin.Manifest.Name, i)
		}
		if !knownPatchKind(patch.Kind) {
			return nil, fmt.Errorf("plugin %s patch %d uses unknown patch kind %q", plugin.Manifest.Name, i, patch.Kind)
		}
		if !allowed[patch.Kind] {
			return nil, PermissionError{
				Plugin:  plugin.Manifest.Name,
				Kind:    patch.Kind,
				Summary: fmt.Sprintf("plugin %s is not allowed to emit %s patches", plugin.Manifest.Name, patch.Kind),
			}
		}
		if len(patch.Value) == 0 {
			return nil, fmt.Errorf("plugin %s patch %d is missing typed value", plugin.Manifest.Name, i)
		}
		if patch.Source.Plugin == "" {
			patch.Source.Plugin = plugin.Manifest.Name
		}
		if patch.Source.Version == "" {
			patch.Source.Version = plugin.Manifest.Version
		}
		out = append(out, patch)
	}
	return out, nil
}

type PatchSet struct {
	Plugin      string                 `json:"plugin"`
	Version     string                 `json:"version"`
	Source      Source                 `json:"source"`
	Patches     []pluginapi.IRPatch    `json:"patches,omitempty"`
	Diagnostics []pluginapi.Diagnostic `json:"diagnostics,omitempty"`
}

type PatchExplanation struct {
	Plugin     string            `json:"plugin"`
	Version    string            `json:"version"`
	Capability string            `json:"capability,omitempty"`
	Op         pluginapi.PatchOp `json:"op"`
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Summary    string            `json:"summary,omitempty"`
}

type SecurityGroupRulePatch struct {
	SecurityGroupRef string `json:"security_group_ref,omitempty"`
	Direction        string `json:"direction"`
	Protocol         string `json:"protocol"`
	FromPort         int    `json:"from_port,omitempty"`
	ToPort           int    `json:"to_port,omitempty"`
	Source           string `json:"source,omitempty"`
	Destination      string `json:"destination,omitempty"`
	Description      string `json:"description,omitempty"`
}

func ApplyIRPatches(graph *ir.Graph, sets []PatchSet) error {
	if graph == nil {
		return fmt.Errorf("graph is required")
	}
	for _, set := range sets {
		for _, patch := range set.Patches {
			switch patch.Kind {
			case PatchKindSecurityGroupRule:
				if err := applySecurityGroupRulePatch(graph, patch); err != nil {
					return fmt.Errorf("plugin %s %s patch failed: %w", set.Plugin, patch.Kind, err)
				}
			default:
				return fmt.Errorf("unsupported patch kind %q", patch.Kind)
			}
		}
	}
	return nil
}

func ExplainPatchSets(sets []PatchSet) []PatchExplanation {
	var out []PatchExplanation
	for _, set := range sets {
		for _, patch := range set.Patches {
			out = append(out, PatchExplanation{
				Plugin:     set.Plugin,
				Version:    set.Version,
				Capability: patch.Source.Capability,
				Op:         patch.Op,
				Path:       patch.Path,
				Kind:       patch.Kind,
				Summary:    patch.Summary,
			})
		}
	}
	return out
}

func applySecurityGroupRulePatch(graph *ir.Graph, patch pluginapi.IRPatch) error {
	var value SecurityGroupRulePatch
	if err := json.Unmarshal(patch.Value, &value); err != nil {
		return fmt.Errorf("decode security group rule: %w", err)
	}
	if value.SecurityGroupRef == "" {
		value.SecurityGroupRef = securityGroupRefFromPath(patch.Path)
	}
	if value.SecurityGroupRef == "" {
		return fmt.Errorf("security_group_ref is required")
	}
	if value.Direction != "ingress" && value.Direction != "egress" {
		return fmt.Errorf("direction must be ingress or egress")
	}
	if value.Protocol == "" {
		return fmt.Errorf("protocol is required")
	}
	if value.ToPort == 0 {
		value.ToPort = value.FromPort
	}
	rule := ir.SecurityRule{
		Direction:   value.Direction,
		Protocol:    value.Protocol,
		FromPort:    value.FromPort,
		ToPort:      value.ToPort,
		Source:      value.Source,
		Destination: value.Destination,
		Description: firstNonEmpty(value.Description, patch.Summary),
	}
	for i := range graph.Resources.SecurityGroups {
		if graph.Resources.SecurityGroups[i].Meta.LogicalID == value.SecurityGroupRef {
			graph.Resources.SecurityGroups[i].Rules = append(graph.Resources.SecurityGroups[i].Rules, rule)
			if patch.Source.Plugin != "" {
				graph.Resources.SecurityGroups[i].Meta.Source = append(graph.Resources.SecurityGroups[i].Meta.Source, ir.SourceRef{Path: "plugin:" + patch.Source.Plugin})
			}
			return nil
		}
	}
	return fmt.Errorf("security group %q was not found", value.SecurityGroupRef)
}

func securityGroupRefFromPath(path string) string {
	const prefix = "/resources/security_groups/"
	const suffix = "/rules/-"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
}

func knownPatchKind(kind string) bool {
	switch kind {
	case PatchKindSecurityGroupRule,
		ir.ResourceKindWorkloadIdentity,
		ir.ResourceKindIAMRole,
		ir.ResourceKindSecurityGroup,
		ir.ResourceKindLogConfig,
		ir.ResourceKindMetricConfig,
		ir.ResourceKindTargetGroup,
		ir.ResourceKindListener,
		ir.ResourceKindManagedDatabase,
		ir.ResourceKindDatabaseSecret,
		ir.ResourceKindDatabaseBinding,
		ir.ResourceKindGlobalTraffic,
		ir.ResourceKindInstanceTemplate,
		ir.ResourceKindAutoscalingGroup,
		ir.ResourceKindRuntimeManifest:
		return true
	default:
		return false
	}
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
