package ir

import (
	"bytes"
	"encoding/json"
	"sort"
)

type Diff struct {
	Changed bool     `json:"changed"`
	Changes []Change `json:"changes,omitempty"`
}

type Change struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

func SemanticDiff(left, right Graph) (Diff, error) {
	leftBody, err := json.Marshal(NormalizeGraph(left))
	if err != nil {
		return Diff{}, err
	}
	rightBody, err := json.Marshal(NormalizeGraph(right))
	if err != nil {
		return Diff{}, err
	}
	if bytes.Equal(leftBody, rightBody) {
		return Diff{}, nil
	}
	return Diff{
		Changed: true,
		Changes: []Change{
			{
				Path:    "$",
				Kind:    "graph_changed",
				Summary: "IR graphs differ semantically",
			},
		},
	}, nil
}

func SemanticallyEqual(left, right Graph) bool {
	diff, err := SemanticDiff(left, right)
	return err == nil && !diff.Changed
}

func NormalizeGraph(graph Graph) Graph {
	out := graph
	out.Resources.WorkloadIdentities = append([]WorkloadIdentity(nil), graph.Resources.WorkloadIdentities...)
	out.Resources.IAMRoles = append([]IAMRole(nil), graph.Resources.IAMRoles...)
	out.Resources.SecurityGroups = append([]SecurityGroup(nil), graph.Resources.SecurityGroups...)
	out.Resources.LogConfigs = append([]LogConfig(nil), graph.Resources.LogConfigs...)
	out.Resources.MetricConfigs = append([]MetricConfig(nil), graph.Resources.MetricConfigs...)
	out.Resources.TargetGroups = append([]TargetGroup(nil), graph.Resources.TargetGroups...)
	out.Resources.Listeners = append([]Listener(nil), graph.Resources.Listeners...)
	out.Resources.ManagedDatabases = append([]ManagedDatabase(nil), graph.Resources.ManagedDatabases...)
	out.Resources.DatabaseSecrets = append([]DatabaseSecret(nil), graph.Resources.DatabaseSecrets...)
	out.Resources.DatabaseBindings = append([]DatabaseBinding(nil), graph.Resources.DatabaseBindings...)
	out.Resources.InstanceTemplates = append([]InstanceTemplate(nil), graph.Resources.InstanceTemplates...)
	out.Resources.AutoscalingGroups = append([]AutoscalingGroup(nil), graph.Resources.AutoscalingGroups...)
	out.Resources.RuntimeManifests = append([]RuntimeManifest(nil), graph.Resources.RuntimeManifests...)
	sort.Slice(out.Resources.WorkloadIdentities, func(i, j int) bool {
		return out.Resources.WorkloadIdentities[i].Meta.LogicalID < out.Resources.WorkloadIdentities[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.IAMRoles, func(i, j int) bool {
		return out.Resources.IAMRoles[i].Meta.LogicalID < out.Resources.IAMRoles[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.SecurityGroups, func(i, j int) bool {
		return out.Resources.SecurityGroups[i].Meta.LogicalID < out.Resources.SecurityGroups[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.LogConfigs, func(i, j int) bool {
		return out.Resources.LogConfigs[i].Meta.LogicalID < out.Resources.LogConfigs[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.MetricConfigs, func(i, j int) bool {
		return out.Resources.MetricConfigs[i].Meta.LogicalID < out.Resources.MetricConfigs[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.TargetGroups, func(i, j int) bool {
		return out.Resources.TargetGroups[i].Meta.LogicalID < out.Resources.TargetGroups[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.Listeners, func(i, j int) bool {
		return out.Resources.Listeners[i].Meta.LogicalID < out.Resources.Listeners[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.ManagedDatabases, func(i, j int) bool {
		return out.Resources.ManagedDatabases[i].Meta.LogicalID < out.Resources.ManagedDatabases[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.DatabaseSecrets, func(i, j int) bool {
		return out.Resources.DatabaseSecrets[i].Meta.LogicalID < out.Resources.DatabaseSecrets[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.DatabaseBindings, func(i, j int) bool {
		return out.Resources.DatabaseBindings[i].Meta.LogicalID < out.Resources.DatabaseBindings[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.InstanceTemplates, func(i, j int) bool {
		return out.Resources.InstanceTemplates[i].Meta.LogicalID < out.Resources.InstanceTemplates[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.AutoscalingGroups, func(i, j int) bool {
		return out.Resources.AutoscalingGroups[i].Meta.LogicalID < out.Resources.AutoscalingGroups[j].Meta.LogicalID
	})
	sort.Slice(out.Resources.RuntimeManifests, func(i, j int) bool {
		return out.Resources.RuntimeManifests[i].Meta.LogicalID < out.Resources.RuntimeManifests[j].Meta.LogicalID
	})
	return out
}
