package compiler

import (
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
)

func compileMultiRegionStack(doc spec.Document, opts Options) (*ir.Graph, error) {
	if doc.MultiRegion == nil {
		return nil, fmt.Errorf("multi-region stack settings are required")
	}
	stack := doc.MultiRegion
	regions := multiRegionRegions(*stack)
	graph := &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       doc.Metadata.Name,
		Env:           doc.Metadata.Env,
	}
	var traffic []ir.TrafficRegion
	primaryDatabaseRef := ""
	weights := trafficWeights(*stack, regions)
	for _, region := range regions {
		regionalDoc := regionalStackDocument(doc, *stack, region)
		regionalGraph, err := compileStack(regionalDoc, opts)
		if err != nil {
			return nil, err
		}
		applyRegionTag(regionalGraph, region)
		role := "replica"
		replicaOf := primaryDatabaseRef
		if region == stack.PrimaryRegion {
			role = "primary"
			replicaOf = ""
		}
		for i := range regionalGraph.Resources.ManagedDatabases {
			db := &regionalGraph.Resources.ManagedDatabases[i]
			db.Region = region
			db.Role = role
			db.PrimaryRegion = stack.PrimaryRegion
			db.ReplicationMode = strings.ToLower(strings.TrimSpace(stack.DatabaseReplication.Mode))
			db.ReplicaOfRef = replicaOf
			db.FailoverPolicy = ir.FailoverPolicy{
				MaxReplicaLag:   stack.FailoverPolicy.MaxReplicaLag,
				FreezeWrites:    stack.FailoverPolicy.FreezeWrites,
				RequireApproval: stack.FailoverPolicy.RequireApproval,
			}
			if role == "primary" {
				primaryDatabaseRef = db.Meta.LogicalID
			}
		}
		traffic = append(traffic, ir.TrafficRegion{
			Region:         region,
			ServiceRef:     regionalGraph.Service,
			TargetGroupRef: firstTargetGroupRef(regionalGraph),
			Weight:         weights[region],
		})
		appendRegionalResources(graph, regionalGraph.Resources)
	}
	graph.Resources.GlobalTraffic = append(graph.Resources.GlobalTraffic, ir.GlobalTraffic{
		Meta:          meta("global-traffic:"+doc.Metadata.Name, ir.ResourceKindGlobalTraffic, resourceName(doc.Metadata.Env, doc.Metadata.Name, "global-traffic"), ir.RequiredTags(doc.Metadata.Name, doc.Metadata.Env), "$.multiRegion.trafficPolicy"),
		Host:          stack.TrafficPolicy.Host,
		Mode:          strings.ToLower(strings.TrimSpace(stack.TrafficPolicy.Mode)),
		PrimaryRegion: stack.PrimaryRegion,
		Regions:       traffic,
	})
	return graph, nil
}

func regionalStackDocument(doc spec.Document, stack spec.MultiRegionStack, region string) spec.Document {
	database := stack.Database
	database.Region = region
	labels := cloneMap(doc.Metadata.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels["region"] = region
	return spec.Document{
		APIVersion: doc.APIVersion,
		Kind:       spec.KindStack,
		Metadata: spec.Metadata{
			Name:   doc.Metadata.Name + "-" + region,
			Env:    doc.Metadata.Env,
			Labels: labels,
		},
		Stack: &spec.Stack{
			Services:  []spec.StackService{stack.Service},
			Databases: []spec.StackDatabase{database},
			Bindings:  []spec.StackBinding{stack.Binding},
		},
	}
}

func multiRegionRegions(stack spec.MultiRegionStack) []string {
	regions := []string{stack.PrimaryRegion}
	regions = append(regions, stack.SecondaryRegions...)
	return regions
}

func trafficWeights(stack spec.MultiRegionStack, regions []string) map[string]int {
	out := make(map[string]int, len(regions))
	for _, region := range regions {
		out[region] = 0
	}
	out[stack.PrimaryRegion] = 100
	for _, weight := range stack.TrafficPolicy.Weights {
		out[weight.Region] = weight.Weight
	}
	return out
}

func firstTargetGroupRef(graph *ir.Graph) string {
	if graph == nil || len(graph.Resources.TargetGroups) == 0 {
		return ""
	}
	return graph.Resources.TargetGroups[0].Meta.LogicalID
}

func applyRegionTag(graph *ir.Graph, region string) {
	forEachMeta(&graph.Resources, func(meta *ir.ResourceMeta) {
		if meta.Tags == nil {
			meta.Tags = map[string]string{}
		}
		meta.Tags[ir.TagRegion] = region
	})
}

func appendRegionalResources(graph *ir.Graph, resources ir.Resources) {
	graph.Resources.WorkloadIdentities = append(graph.Resources.WorkloadIdentities, resources.WorkloadIdentities...)
	graph.Resources.IAMRoles = append(graph.Resources.IAMRoles, resources.IAMRoles...)
	graph.Resources.SecurityGroups = append(graph.Resources.SecurityGroups, resources.SecurityGroups...)
	graph.Resources.LogConfigs = append(graph.Resources.LogConfigs, resources.LogConfigs...)
	graph.Resources.MetricConfigs = append(graph.Resources.MetricConfigs, resources.MetricConfigs...)
	graph.Resources.TargetGroups = append(graph.Resources.TargetGroups, resources.TargetGroups...)
	graph.Resources.Listeners = append(graph.Resources.Listeners, resources.Listeners...)
	graph.Resources.ManagedDatabases = append(graph.Resources.ManagedDatabases, resources.ManagedDatabases...)
	graph.Resources.DatabaseSecrets = append(graph.Resources.DatabaseSecrets, resources.DatabaseSecrets...)
	graph.Resources.DatabaseBindings = append(graph.Resources.DatabaseBindings, resources.DatabaseBindings...)
	graph.Resources.InstanceTemplates = append(graph.Resources.InstanceTemplates, resources.InstanceTemplates...)
	graph.Resources.AutoscalingGroups = append(graph.Resources.AutoscalingGroups, resources.AutoscalingGroups...)
	graph.Resources.RuntimeManifests = append(graph.Resources.RuntimeManifests, resources.RuntimeManifests...)
}

func forEachMeta(resources *ir.Resources, fn func(*ir.ResourceMeta)) {
	for i := range resources.WorkloadIdentities {
		fn(&resources.WorkloadIdentities[i].Meta)
	}
	for i := range resources.IAMRoles {
		fn(&resources.IAMRoles[i].Meta)
	}
	for i := range resources.SecurityGroups {
		fn(&resources.SecurityGroups[i].Meta)
	}
	for i := range resources.LogConfigs {
		fn(&resources.LogConfigs[i].Meta)
	}
	for i := range resources.MetricConfigs {
		fn(&resources.MetricConfigs[i].Meta)
	}
	for i := range resources.TargetGroups {
		fn(&resources.TargetGroups[i].Meta)
	}
	for i := range resources.Listeners {
		fn(&resources.Listeners[i].Meta)
	}
	for i := range resources.ManagedDatabases {
		fn(&resources.ManagedDatabases[i].Meta)
	}
	for i := range resources.DatabaseSecrets {
		fn(&resources.DatabaseSecrets[i].Meta)
	}
	for i := range resources.DatabaseBindings {
		fn(&resources.DatabaseBindings[i].Meta)
	}
	for i := range resources.GlobalTraffic {
		fn(&resources.GlobalTraffic[i].Meta)
	}
	for i := range resources.InstanceTemplates {
		fn(&resources.InstanceTemplates[i].Meta)
	}
	for i := range resources.AutoscalingGroups {
		fn(&resources.AutoscalingGroups[i].Meta)
	}
	for i := range resources.RuntimeManifests {
		fn(&resources.RuntimeManifests[i].Meta)
	}
}
