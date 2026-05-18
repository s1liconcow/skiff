package cost

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
)

const (
	SeverityInfo   = "info"
	SeverityMedium = "medium"
	SeverityHigh   = "high"

	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
)

var shapeCatalog = []ShapeInfo{
	{Name: "small", VCPU: 2, MemoryGB: 2, RelativeCost: 1},
	{Name: "medium", VCPU: 2, MemoryGB: 4, RelativeCost: 2},
	{Name: "large", VCPU: 2, MemoryGB: 8, RelativeCost: 4},
}

func Analyze(input Input) Result {
	input.Shape = normalizeShape(input.Shape)
	shape := shapeInfo(input.Shape.MachineSize)
	result := Result{
		Service: input.Shape.Service,
		Env:     input.Shape.Env,
		Shape:   shape,
		Scale:   ScaleInfo{MinReplicas: input.Shape.MinReplicas, MaxReplicas: input.Shape.MaxReplicas},
	}
	result.Observations = observations(input.Signals)
	result.Recommendations = append(result.Recommendations, utilizationRecommendations(input, shape)...)
	result.Recommendations = append(result.Recommendations, replicaRecommendations(input)...)
	result.Recommendations = append(result.Recommendations, logRecommendations(input)...)
	if len(result.Recommendations) == 0 {
		result.Recommendations = append(result.Recommendations, Recommendation{
			ID:         "cost.no_actionable_change",
			Category:   "summary",
			Severity:   SeverityInfo,
			Summary:    "no obvious shape or replica change from the supplied evidence",
			Confidence: confidenceForSignals(input.Signals),
			Evidence:   result.Observations,
		})
	}
	result.Limitations = limitations(input)
	return result
}

func PlanWarnings(input Input) []Recommendation {
	input.Shape = normalizeShape(input.Shape)
	var out []Recommendation
	if input.Shape.MinReplicas >= 8 {
		out = append(out, Recommendation{
			ID:              "cost.plan.high_min_replicas",
			Category:        "replicas",
			Severity:        SeverityMedium,
			Summary:         fmt.Sprintf("minimum replicas is %d; Skiff runs one VM per workload replica by default", input.Shape.MinReplicas),
			Confidence:      ConfidenceLow,
			EstimatedImpact: "review whether warm capacity needs this many always-on VMs before deploy",
			Evidence:        []Evidence{intEvidence("min_replicas", input.Shape.MinReplicas, "replicas", "desired minimum from spec")},
		})
	}
	shapeIndex := indexOfShape(input.Shape.MachineSize)
	if shapeIndex >= indexOfShape("large") {
		out = append(out, Recommendation{
			ID:              "cost.plan.large_shape",
			Category:        "shape",
			Severity:        SeverityMedium,
			Summary:         fmt.Sprintf("machine size %s may be expensive when multiplied by replicas", input.Shape.MachineSize),
			Confidence:      ConfidenceLow,
			EstimatedImpact: "confirm the service needs this VM shape before every replica gets it",
			Evidence:        []Evidence{{Metric: "machine_size", Value: input.Shape.MachineSize, Summary: "desired machine size from spec"}},
		})
	}
	if shapeIndex < 0 {
		out = append(out, Recommendation{
			ID:              "cost.plan.custom_shape",
			Category:        "shape",
			Severity:        SeverityInfo,
			Summary:         fmt.Sprintf("machine size %s is provider-specific and needs explicit cost review", input.Shape.MachineSize),
			Confidence:      ConfidenceLow,
			EstimatedImpact: "provider-specific shapes may be larger or more expensive than Skiff's named defaults",
			Evidence:        []Evidence{{Metric: "machine_size", Value: input.Shape.MachineSize, Summary: "desired machine size from spec"}},
		})
	}
	if input.Shape.MaxReplicas > 0 && input.Shape.MaxReplicas == input.Shape.MinReplicas && input.Shape.MinReplicas >= 4 {
		out = append(out, Recommendation{
			ID:              "cost.plan.fixed_warm_capacity",
			Category:        "warm_capacity",
			Severity:        SeverityInfo,
			Summary:         fmt.Sprintf("min and max replicas are both %d, so all capacity is always warm", input.Shape.MinReplicas),
			Confidence:      ConfidenceLow,
			EstimatedImpact: "use observed traffic and SLOs to decide whether some capacity can scale out instead",
			Evidence:        []Evidence{intEvidence("max_replicas", input.Shape.MaxReplicas, "replicas", "desired maximum from spec")},
		})
	}
	return out
}

func InputFromGraph(graph *ir.Graph) Input {
	if graph == nil {
		return Input{}
	}
	shape := ServiceShape{Service: graph.Service, Env: graph.Env}
	if len(graph.Resources.InstanceTemplates) > 0 {
		tmpl := graph.Resources.InstanceTemplates[0]
		shape.MachineSize = tmpl.Machine.Size
		shape.MachineArch = tmpl.Machine.Arch
	}
	if len(graph.Resources.AutoscalingGroups) > 0 {
		asg := graph.Resources.AutoscalingGroups[0]
		shape.MinReplicas = asg.Min
		shape.MaxReplicas = asg.Max
	}
	if len(graph.Resources.StatefulGroups) > 0 {
		group := graph.Resources.StatefulGroups[0]
		shape.MachineSize = "small"
		shape.MachineArch = "x86_64"
		shape.MinReplicas = group.Replicas
		shape.MaxReplicas = group.Replicas
		if shape.MinReplicas == 0 {
			shape.MinReplicas = len(graph.Resources.StatefulMembers)
			shape.MaxReplicas = shape.MinReplicas
		}
		if recipe := firstStatefulRecipe(graph); recipe != nil {
			shape.MetricsEnabled = recipe.Metrics.Enabled
		}
		shape.LogsEnabled = true
	}
	if len(graph.Resources.LogConfigs) > 0 {
		shape.LogsEnabled = graph.Resources.LogConfigs[0].Enabled
	}
	if len(graph.Resources.MetricConfigs) > 0 {
		shape.MetricsEnabled = graph.Resources.MetricConfigs[0].Enabled
	}
	return Input{Shape: normalizeShape(shape)}
}

func firstStatefulRecipe(graph *ir.Graph) *ir.StatefulRecipe {
	if graph == nil || len(graph.Resources.StatefulRecipes) == 0 {
		return nil
	}
	return &graph.Resources.StatefulRecipes[0]
}

func KnownShapes() []ShapeInfo {
	out := append([]ShapeInfo(nil), shapeCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].RelativeCost < out[j].RelativeCost })
	return out
}

func utilizationRecommendations(input Input, shape ShapeInfo) []Recommendation {
	cpu := input.Signals.CPUP95Percent
	mem := input.Signals.MemoryP95Percent
	if cpu == nil && mem == nil {
		return nil
	}
	evidence := utilizationEvidence(input.Signals)
	idx := indexOfShape(shape.Name)
	if lowUtilization(cpu, mem) && idx > 0 {
		smaller := shapeCatalog[idx-1]
		return []Recommendation{{
			ID:              "cost.shape.downsize",
			Category:        "shape",
			Severity:        SeverityMedium,
			Summary:         fmt.Sprintf("try size %s instead of %s after validating SLOs", smaller.Name, shape.Name),
			Confidence:      confidenceForUtilization(cpu, mem),
			EstimatedImpact: fmt.Sprintf("reduces each replica from relative shape cost %d to %d; this is not a billing estimate", shape.RelativeCost, smaller.RelativeCost),
			Evidence:        evidence,
		}}
	}
	if highUtilization(cpu, mem) {
		if idx >= 0 && idx+1 < len(shapeCatalog) {
			larger := shapeCatalog[idx+1]
			return []Recommendation{{
				ID:              "cost.shape.upsize",
				Category:        "shape",
				Severity:        SeverityHigh,
				Summary:         fmt.Sprintf("evaluate size %s instead of %s before increasing replica count", larger.Name, shape.Name),
				Confidence:      confidenceForUtilization(cpu, mem),
				EstimatedImpact: "may reduce saturation risk while preserving the one-VM-per-replica model",
				Evidence:        evidence,
			}}
		}
		return []Recommendation{{
			ID:              "cost.shape.saturated_custom",
			Category:        "shape",
			Severity:        SeverityHigh,
			Summary:         "observed utilization is high; evaluate a larger VM shape or higher max replicas",
			Confidence:      confidenceForUtilization(cpu, mem),
			EstimatedImpact: "reduces saturation risk; requires provider-specific pricing review",
			Evidence:        evidence,
		}}
	}
	return nil
}

func replicaRecommendations(input Input) []Recommendation {
	var out []Recommendation
	minReplicas := input.Shape.MinReplicas
	maxReplicas := input.Shape.MaxReplicas
	if input.Signals.WarmCapacity != nil && *input.Signals.WarmCapacity > 0 && minReplicas > *input.Signals.WarmCapacity {
		out = append(out, Recommendation{
			ID:              "cost.replicas.reduce_min",
			Category:        "replicas",
			Severity:        SeverityMedium,
			Summary:         fmt.Sprintf("reduce min replicas from %d toward observed warm capacity %d after validating SLOs", minReplicas, *input.Signals.WarmCapacity),
			Confidence:      ConfidenceMedium,
			EstimatedImpact: "reduces always-on VM count while keeping explicit warm capacity",
			Evidence:        []Evidence{intEvidence("warm_capacity", *input.Signals.WarmCapacity, "replicas", "operator-supplied warm capacity target"), intEvidence("min_replicas", minReplicas, "replicas", "desired minimum from spec")},
		})
	} else if input.Signals.RequestRateRPS != nil && input.Signals.CPUP95Percent != nil && *input.Signals.CPUP95Percent < 35 && minReplicas >= 4 {
		target := int(math.Ceil(*input.Signals.RequestRateRPS / 25.0))
		if target < 2 {
			target = 2
		}
		if target < minReplicas {
			out = append(out, Recommendation{
				ID:              "cost.replicas.review_min",
				Category:        "replicas",
				Severity:        SeverityMedium,
				Summary:         fmt.Sprintf("review reducing min replicas from %d toward %d after load testing", minReplicas, target),
				Confidence:      ConfidenceLow,
				EstimatedImpact: "traffic-derived target is approximate; validate against latency and failover requirements",
				Evidence:        []Evidence{floatEvidence("request_rate", *input.Signals.RequestRateRPS, "rps", "observed request rate"), floatEvidence("cpu_p95", *input.Signals.CPUP95Percent, "%", "observed CPU p95")},
			})
		}
	}
	if input.Signals.UnhealthyTargets != nil && *input.Signals.UnhealthyTargets > 0 && maxReplicas <= minReplicas {
		out = append(out, Recommendation{
			ID:              "cost.warm_capacity.add_headroom",
			Category:        "warm_capacity",
			Severity:        SeverityHigh,
			Summary:         fmt.Sprintf("increase max replicas above %d so replacements or traffic spikes have headroom", maxReplicas),
			Confidence:      ConfidenceMedium,
			EstimatedImpact: "improves resilience; may increase spend only when scale-out is used",
			Evidence:        []Evidence{intEvidence("unhealthy_targets", *input.Signals.UnhealthyTargets, "targets", "observed unhealthy load balancer targets"), intEvidence("max_replicas", maxReplicas, "replicas", "desired maximum from spec")},
		})
	}
	return out
}

func logRecommendations(input Input) []Recommendation {
	if input.Signals.LogMegabytesPerHour == nil {
		return nil
	}
	value := *input.Signals.LogMegabytesPerHour
	switch {
	case value >= 1024:
		return []Recommendation{{
			ID:              "cost.logs.noisy",
			Category:        "logs",
			Severity:        SeverityMedium,
			Summary:         "log volume is high; reduce noisy log lines or lower debug verbosity",
			Confidence:      ConfidenceHigh,
			EstimatedImpact: "can lower CloudWatch ingestion and retention pressure; estimate requires provider pricing",
			Evidence:        []Evidence{floatEvidence("log_volume", value, "MB/hour", "observed log ingestion volume")},
		}}
	case value >= 256:
		return []Recommendation{{
			ID:              "cost.logs.watch",
			Category:        "logs",
			Severity:        SeverityInfo,
			Summary:         "log volume is noticeable; watch ingestion before scaling replicas",
			Confidence:      ConfidenceMedium,
			EstimatedImpact: "prevents per-replica log growth from surprising operators",
			Evidence:        []Evidence{floatEvidence("log_volume", value, "MB/hour", "observed log ingestion volume")},
		}}
	default:
		return nil
	}
}

func observations(signals ObservedSignals) []Evidence {
	var out []Evidence
	if signals.CPUP95Percent != nil {
		out = append(out, floatEvidence("cpu_p95", *signals.CPUP95Percent, "%", "observed CPU p95"))
	}
	if signals.MemoryP95Percent != nil {
		out = append(out, floatEvidence("memory_p95", *signals.MemoryP95Percent, "%", "observed memory p95"))
	}
	if signals.RequestRateRPS != nil {
		out = append(out, floatEvidence("request_rate", *signals.RequestRateRPS, "rps", "observed request rate"))
	}
	if signals.RequestCount != nil {
		out = append(out, floatEvidence("request_count", *signals.RequestCount, "requests", "observed request count"))
	}
	if signals.UnhealthyTargets != nil {
		out = append(out, intEvidence("unhealthy_targets", *signals.UnhealthyTargets, "targets", "observed unhealthy load balancer targets"))
	}
	if signals.WarmCapacity != nil {
		out = append(out, intEvidence("warm_capacity", *signals.WarmCapacity, "replicas", "operator-supplied warm capacity target"))
	}
	if signals.LogMegabytesPerHour != nil {
		out = append(out, floatEvidence("log_volume", *signals.LogMegabytesPerHour, "MB/hour", "observed log ingestion volume"))
	}
	if signals.Window != "" {
		out = append(out, Evidence{Metric: "window", Value: signals.Window, Summary: "observation window"})
	}
	return out
}

func limitations(input Input) []string {
	limits := []string{
		"recommendations are relative shape and capacity guidance, not billing truth",
		"validate every change against service SLOs before mutating production capacity",
	}
	if input.Signals.CPUP95Percent == nil || input.Signals.MemoryP95Percent == nil {
		limits = append(limits, "shape confidence improves when both CPU and memory p95 are supplied")
	}
	if input.Signals.RequestRateRPS == nil && input.Signals.RequestCount == nil && input.Signals.WarmCapacity == nil {
		limits = append(limits, "replica guidance is conservative without request rate or warm-capacity evidence")
	}
	if input.Signals.LogMegabytesPerHour == nil {
		limits = append(limits, "log cost guidance needs observed log volume")
	}
	return limits
}

func normalizeShape(shape ServiceShape) ServiceShape {
	shape.MachineSize = strings.TrimSpace(shape.MachineSize)
	if shape.MachineSize == "" {
		shape.MachineSize = "small"
	}
	if shape.MinReplicas == 0 {
		shape.MinReplicas = 1
	}
	if shape.MaxReplicas == 0 {
		shape.MaxReplicas = shape.MinReplicas
	}
	return shape
}

func shapeInfo(name string) ShapeInfo {
	idx := indexOfShape(name)
	if idx >= 0 {
		return shapeCatalog[idx]
	}
	return ShapeInfo{Name: name, RelativeCost: 0}
}

func indexOfShape(name string) int {
	for i, shape := range shapeCatalog {
		if shape.Name == name {
			return i
		}
	}
	return -1
}

func lowUtilization(cpu, mem *float64) bool {
	cpuOK := cpu == nil || *cpu <= 25
	memOK := mem == nil || *mem <= 55
	return cpuOK && memOK
}

func highUtilization(cpu, mem *float64) bool {
	return (cpu != nil && *cpu >= 80) || (mem != nil && *mem >= 85)
}

func confidenceForUtilization(cpu, mem *float64) string {
	if cpu != nil && mem != nil {
		return ConfidenceHigh
	}
	return ConfidenceMedium
}

func confidenceForSignals(signals ObservedSignals) string {
	if signals.CPUP95Percent != nil && signals.MemoryP95Percent != nil && (signals.RequestRateRPS != nil || signals.WarmCapacity != nil) {
		return ConfidenceHigh
	}
	if signals.CPUP95Percent != nil || signals.MemoryP95Percent != nil || signals.LogMegabytesPerHour != nil {
		return ConfidenceMedium
	}
	return ConfidenceLow
}

func utilizationEvidence(signals ObservedSignals) []Evidence {
	var evidence []Evidence
	if signals.CPUP95Percent != nil {
		evidence = append(evidence, floatEvidence("cpu_p95", *signals.CPUP95Percent, "%", "observed CPU p95"))
	}
	if signals.MemoryP95Percent != nil {
		evidence = append(evidence, floatEvidence("memory_p95", *signals.MemoryP95Percent, "%", "observed memory p95"))
	}
	return evidence
}

func floatEvidence(metric string, value float64, unit, summary string) Evidence {
	return Evidence{Metric: metric, Value: fmt.Sprintf("%.1f", value), Unit: unit, Summary: summary}
}

func intEvidence(metric string, value int, unit, summary string) Evidence {
	return Evidence{Metric: metric, Value: fmt.Sprintf("%d", value), Unit: unit, Summary: summary}
}
