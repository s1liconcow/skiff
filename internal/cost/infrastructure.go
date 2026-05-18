package cost

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
)

func EstimateInfrastructure(graph *ir.Graph, catalog PricingCatalog, opts PricingOptions) InfraEstimate {
	if opts.MonthlyHours <= 0 {
		opts.MonthlyHours = DefaultMonthlyHours
	}
	estimate := InfraEstimate{
		Provider:        catalog.Provider,
		Region:          catalog.Region,
		Currency:        firstNonEmpty(catalog.Currency, "USD"),
		Source:          catalog.Source,
		PublicationDate: catalog.PublicationDate,
		Version:         catalog.Version,
		MonthlyHours:    opts.MonthlyHours,
	}
	if graph == nil {
		estimate.Notes = append(estimate.Notes, "no compiled graph was available for infrastructure cost estimation")
		return estimate
	}
	addComputeLineItems(&estimate, graph, catalog, opts)
	addStatefulStorageLineItems(&estimate, graph, catalog)
	addSupportLineItems(&estimate, graph)
	estimate.Totals = infraTotals(estimate.LineItems, opts.MonthlyHours)
	estimate.Scenarios = usageScenarios(graph, catalog, opts)
	if hasStatefulSnapshots(graph) {
		estimate.Notes = append(estimate.Notes, "snapshot storage estimate assumes stored snapshot data equals provisioned stateful volume size; incremental changed blocks and retention can change real spend")
	}
	if hasUsageBasedLineItems(estimate.LineItems) {
		estimate.Notes = append(estimate.Notes, "usage-based resources are listed but excluded from totals unless Skiff has enough usage data to estimate them")
	}
	return estimate
}

func usageScenarios(graph *ir.Graph, catalog PricingCatalog, opts PricingOptions) []UsageScenario {
	if opts.MonthlyHours <= 0 {
		opts.MonthlyHours = DefaultMonthlyHours
	}
	machineSize, minReplicas, maxReplicas := computeShapeFromGraph(graph)
	if minReplicas <= 0 {
		return nil
	}
	if maxReplicas < minReplicas {
		maxReplicas = minReplicas
	}
	totalVolumeGB := statefulVolumeGB(graph)
	snapshotsEnabled := hasStatefulSnapshots(graph) && totalVolumeGB > 0
	defs := []struct {
		name           string
		replicas       int
		snapshotFactor float64
		summary        string
	}{
		{name: "low", replicas: minReplicas, snapshotFactor: 0.25, summary: "baseline utilization"},
		{name: "medium", replicas: midpointReplicas(minReplicas, maxReplicas), snapshotFactor: 0.50, summary: "moderate utilization"},
		{name: "high", replicas: maxReplicas, snapshotFactor: 1.00, summary: "high utilization"},
	}
	out := make([]UsageScenario, 0, len(defs))
	for _, def := range defs {
		scenario := UsageScenario{
			Name:            def.name,
			Summary:         def.summary,
			AssumedReplicas: def.replicas,
			Assumptions: []string{
				fmt.Sprintf("compute uses %d active workload VM(s)", def.replicas),
				"provisioned EBS volume storage is treated as fixed infrastructure",
			},
		}
		snapshotGB := 0.0
		if snapshotsEnabled {
			snapshotGB = totalVolumeGB * def.snapshotFactor
			scenario.SnapshotDataGB = roundMoney(snapshotGB)
			scenario.SnapshotDataPercent = roundMoney(def.snapshotFactor * 100)
			scenario.Assumptions = append(scenario.Assumptions, fmt.Sprintf("EBS snapshot storage uses %.0f%% of provisioned stateful volume size", def.snapshotFactor*100))
		}
		scenario.Totals = scenarioTotals(machineSize, def.replicas, snapshotGB, graph, catalog, opts)
		out = append(out, scenario)
	}
	return out
}

func scenarioTotals(machineSize string, replicas int, snapshotGB float64, graph *ir.Graph, catalog PricingCatalog, opts PricingOptions) []InfraTotal {
	if opts.MonthlyHours <= 0 {
		opts.MonthlyHours = DefaultMonthlyHours
	}
	item, ok := catalog.itemForMachineSize(machineSize)
	if !ok {
		item, ok = catalog.itemForInstanceType(machineSize)
	}
	if !ok || len(item.Rates) == 0 {
		return nil
	}
	storageMonthly := fixedStorageMonthly(graph, catalog)
	snapshotMonthly := snapshotMonthlyCost(snapshotGB, catalog)
	out := make([]InfraTotal, 0, len(item.Rates))
	for _, rate := range item.Rates {
		effectiveHourly := effectiveHourly(rate)
		computeMonthly := effectiveHourly * float64(replicas) * opts.MonthlyHours
		monthly := computeMonthly + storageMonthly + snapshotMonthly
		total := InfraTotal{
			PricingScheme: rate.Scheme,
			MonthlyUSD:    roundMoney(monthly),
			AnnualUSD:     roundMoney(monthly * 12),
		}
		if rate.TermHours > 0 {
			total.TermUSD = roundMoney(monthly * float64(rate.TermHours) / opts.MonthlyHours)
		}
		out = append(out, total)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return pricingSchemeSort(out[i].PricingScheme) < pricingSchemeSort(out[j].PricingScheme)
	})
	return out
}

func addComputeLineItems(estimate *InfraEstimate, graph *ir.Graph, catalog PricingCatalog, opts PricingOptions) {
	machineSize, minReplicas, maxReplicas := computeShapeFromGraph(graph)
	if minReplicas <= 0 {
		return
	}
	item, ok := catalog.itemForMachineSize(machineSize)
	if !ok {
		item, ok = catalog.itemForInstanceType(machineSize)
	}
	if !ok || len(item.Rates) == 0 {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "compute.instances",
			Category:  "compute",
			Kind:      "ec2_instance",
			Name:      machineSize,
			Quantity:  float64(minReplicas),
			Unit:      "instances",
			Estimated: false,
			Summary:   fmt.Sprintf("%d baseline workload VM(s); AWS instance pricing was not available", minReplicas),
		})
		return
	}
	for _, rate := range item.Rates {
		effectiveHourly := effectiveHourly(rate)
		monthly := effectiveHourly * float64(minReplicas) * opts.MonthlyHours
		line := InfraLineItem{
			ID:              "compute.instances." + rate.Scheme,
			Category:        "compute",
			Kind:            "ec2_instance",
			Name:            item.InstanceType,
			PricingScheme:   rate.Scheme,
			Quantity:        float64(minReplicas) * opts.MonthlyHours,
			Unit:            "instance-hour",
			UnitPriceUSD:    roundMoney(effectiveHourly),
			MonthlyUSD:      roundMoney(monthly),
			AnnualUSD:       roundMoney(effectiveHourly * float64(minReplicas) * 8760),
			Estimated:       true,
			IncludedInTotal: true,
			Summary:         fmt.Sprintf("%d baseline %s VM(s)", minReplicas, item.InstanceType),
		}
		if maxReplicas > minReplicas {
			line.Summary = fmt.Sprintf("%d baseline %s VM(s), up to %d when scaled out", minReplicas, item.InstanceType, maxReplicas)
		}
		if rate.TermHours > 0 {
			line.TermUSD = roundMoney((rate.HourlyUSD*float64(rate.TermHours) + rate.UpfrontUSD) * float64(minReplicas))
		}
		estimate.LineItems = append(estimate.LineItems, line)
	}
}

func addStatefulStorageLineItems(estimate *InfraEstimate, graph *ir.Graph, catalog PricingCatalog) {
	totalByType := map[string]float64{}
	for _, volume := range graph.Resources.StatefulVolumes {
		size := parseGiB(volume.Size)
		if size <= 0 {
			estimate.LineItems = append(estimate.LineItems, InfraLineItem{
				ID:        "storage.ebs." + volume.Meta.LogicalID,
				Category:  "storage",
				Kind:      "ebs_volume",
				Name:      volume.Meta.Name,
				Estimated: false,
				Summary:   fmt.Sprintf("stateful volume %s has unparseable size %q", volume.Meta.LogicalID, volume.Size),
			})
			continue
		}
		volumeType := firstNonEmpty(volume.Type, "gp3")
		totalByType[volumeType] += size
	}
	var volumeTypes []string
	for volumeType := range totalByType {
		volumeTypes = append(volumeTypes, volumeType)
	}
	sort.Strings(volumeTypes)
	for _, volumeType := range volumeTypes {
		totalGiB := totalByType[volumeType]
		rate, ok := catalog.storageRate(StorageKindEBSVolumeGBMonth, volumeType)
		if !ok {
			estimate.LineItems = append(estimate.LineItems, InfraLineItem{
				ID:        "storage.ebs." + volumeType,
				Category:  "storage",
				Kind:      "ebs_volume",
				Name:      volumeType,
				Quantity:  totalGiB,
				Unit:      "GB-month",
				Estimated: false,
				Summary:   fmt.Sprintf("%.0f GB-month of %s EBS volume storage; AWS storage pricing was not available", totalGiB, volumeType),
			})
			continue
		}
		monthly := totalGiB * rate.UnitPriceUSD
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:              "storage.ebs." + volumeType,
			Category:        "storage",
			Kind:            "ebs_volume",
			Name:            volumeType,
			Quantity:        roundMoney(totalGiB),
			Unit:            rate.Unit,
			UnitPriceUSD:    roundMoney(rate.UnitPriceUSD),
			MonthlyUSD:      roundMoney(monthly),
			AnnualUSD:       roundMoney(monthly * 12),
			Estimated:       true,
			IncludedInTotal: true,
			Summary:         fmt.Sprintf("%.0f GB-month of %s EBS volume storage", totalGiB, volumeType),
		})
	}
	if !hasStatefulSnapshots(graph) {
		return
	}
	totalGiB := 0.0
	for _, size := range totalByType {
		totalGiB += size
	}
	if totalGiB <= 0 {
		return
	}
	rate, ok := catalog.storageRate(StorageKindEBSSnapshotGBMonth, "")
	if !ok {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "storage.ebs_snapshots",
			Category:  "storage",
			Kind:      "ebs_snapshot",
			Quantity:  roundMoney(totalGiB),
			Unit:      "GB-month",
			Estimated: false,
			Summary:   fmt.Sprintf("snapshot policy enabled for %.0f GB of stateful volumes; AWS snapshot pricing was not available", totalGiB),
		})
		return
	}
	monthly := totalGiB * rate.UnitPriceUSD
	estimate.LineItems = append(estimate.LineItems, InfraLineItem{
		ID:              "storage.ebs_snapshots",
		Category:        "storage",
		Kind:            "ebs_snapshot",
		Quantity:        roundMoney(totalGiB),
		Unit:            rate.Unit,
		UnitPriceUSD:    roundMoney(rate.UnitPriceUSD),
		MonthlyUSD:      roundMoney(monthly),
		AnnualUSD:       roundMoney(monthly * 12),
		Estimated:       true,
		IncludedInTotal: true,
		Summary:         fmt.Sprintf("baseline snapshot storage for %.0f GB of stateful volumes", totalGiB),
	})
}

func addSupportLineItems(estimate *InfraEstimate, graph *ir.Graph) {
	if len(graph.Resources.StatefulDNS) > 0 {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "network.route53_records",
			Category:  "network",
			Kind:      "route53_record",
			Quantity:  float64(len(graph.Resources.StatefulDNS)),
			Unit:      "records",
			Estimated: false,
			Summary:   "Route53 record changes have no durable per-record monthly charge; hosted-zone and query charges are usage-based or external",
		})
	}
	if hasTargetGroup(graph) {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "network.target_group",
			Category:  "network",
			Kind:      "load_balancer_target_group",
			Quantity:  1,
			Unit:      "target groups",
			Estimated: true,
			Summary:   "target group has no direct hourly charge; attached load balancer and LCU charges are not included",
		})
	}
	if hasLogGroup(graph) {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "observability.logs",
			Category:  "observability",
			Kind:      "cloudwatch_logs",
			Quantity:  1,
			Unit:      "log groups",
			Estimated: false,
			Summary:   "CloudWatch Logs ingestion and retention depend on emitted log volume",
		})
	}
	if hasMetrics(graph) {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "observability.metrics",
			Category:  "observability",
			Kind:      "cloudwatch_metrics",
			Quantity:  1,
			Unit:      "metric streams",
			Estimated: false,
			Summary:   "CloudWatch metric costs depend on custom metric count and observation cadence",
		})
	}
	controlCount := len(graph.Resources.WorkloadIdentities) + len(graph.Resources.IAMRoles) + len(graph.Resources.SecurityGroups)
	if len(graph.Resources.StatefulGroups) > 0 {
		controlCount += 4 // IAM role/profile, security group, launch template, and fencing policy class.
	}
	if controlCount > 0 {
		estimate.LineItems = append(estimate.LineItems, InfraLineItem{
			ID:        "control.support_resources",
			Category:  "control",
			Kind:      "iam_security_launch_template",
			Quantity:  float64(controlCount),
			Unit:      "resources",
			Estimated: true,
			Summary:   "IAM, security group, launch template, attachment, and fencing support resources have no direct monthly charge",
		})
	}
}

func infraTotals(lines []InfraLineItem, monthlyHours float64) []InfraTotal {
	if monthlyHours <= 0 {
		monthlyHours = DefaultMonthlyHours
	}
	type acc struct {
		monthly   float64
		annual    float64
		termHours int
	}
	totals := map[string]*acc{}
	var order []string
	for _, line := range lines {
		if !line.Estimated || !line.IncludedInTotal {
			continue
		}
		if line.PricingScheme != "" {
			if _, ok := totals[line.PricingScheme]; !ok {
				totals[line.PricingScheme] = &acc{}
				order = append(order, line.PricingScheme)
			}
			totals[line.PricingScheme].monthly += line.MonthlyUSD
			totals[line.PricingScheme].annual += line.AnnualUSD
			continue
		}
		for scheme, total := range totals {
			total.monthly += line.MonthlyUSD
			total.annual += line.AnnualUSD
			if total.termHours == 0 {
				total.termHours = termHoursFromScheme(scheme)
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return pricingSchemeSort(order[i]) < pricingSchemeSort(order[j])
	})
	out := make([]InfraTotal, 0, len(order))
	for _, scheme := range order {
		total := totals[scheme]
		item := InfraTotal{
			PricingScheme: scheme,
			MonthlyUSD:    roundMoney(total.monthly),
			AnnualUSD:     roundMoney(total.annual),
		}
		if hours := termHoursFromScheme(scheme); hours > 0 {
			item.TermUSD = roundMoney(total.monthly * float64(hours) / monthlyHours)
		}
		out = append(out, item)
	}
	return out
}

func computeShapeFromGraph(graph *ir.Graph) (string, int, int) {
	if graph == nil {
		return "", 0, 0
	}
	if len(graph.Resources.StatefulGroups) > 0 {
		group := graph.Resources.StatefulGroups[0]
		replicas := group.Replicas
		if replicas == 0 {
			replicas = len(graph.Resources.StatefulMembers)
		}
		return "small", replicas, replicas
	}
	if len(graph.Resources.InstanceTemplates) == 0 || len(graph.Resources.AutoscalingGroups) == 0 {
		return "", 0, 0
	}
	machine := graph.Resources.InstanceTemplates[0].Machine
	asg := graph.Resources.AutoscalingGroups[0]
	return firstNonEmpty(machine.Size, "small"), asg.Min, asg.Max
}

func midpointReplicas(minReplicas, maxReplicas int) int {
	if maxReplicas <= minReplicas {
		return minReplicas
	}
	return minReplicas + ((maxReplicas - minReplicas + 1) / 2)
}

func statefulVolumeGB(graph *ir.Graph) float64 {
	if graph == nil {
		return 0
	}
	total := 0.0
	for _, volume := range graph.Resources.StatefulVolumes {
		total += parseGiB(volume.Size)
	}
	return total
}

func fixedStorageMonthly(graph *ir.Graph, catalog PricingCatalog) float64 {
	if graph == nil {
		return 0
	}
	totalByType := map[string]float64{}
	for _, volume := range graph.Resources.StatefulVolumes {
		size := parseGiB(volume.Size)
		if size <= 0 {
			continue
		}
		totalByType[firstNonEmpty(volume.Type, "gp3")] += size
	}
	total := 0.0
	for volumeType, size := range totalByType {
		rate, ok := catalog.storageRate(StorageKindEBSVolumeGBMonth, volumeType)
		if !ok {
			continue
		}
		total += size * rate.UnitPriceUSD
	}
	return total
}

func snapshotMonthlyCost(snapshotGB float64, catalog PricingCatalog) float64 {
	if snapshotGB <= 0 {
		return 0
	}
	rate, ok := catalog.storageRate(StorageKindEBSSnapshotGBMonth, "")
	if !ok {
		return 0
	}
	return snapshotGB * rate.UnitPriceUSD
}

func hasStatefulSnapshots(graph *ir.Graph) bool {
	if graph == nil {
		return false
	}
	for _, policy := range graph.Resources.SnapshotPolicies {
		if policy.Enabled {
			return true
		}
	}
	return false
}

func hasTargetGroup(graph *ir.Graph) bool {
	if graph == nil {
		return false
	}
	if len(graph.Resources.TargetGroups) > 0 {
		return true
	}
	for _, recipe := range graph.Resources.StatefulRecipes {
		if recipe.HealthCheck.Port != 0 {
			return true
		}
	}
	return false
}

func hasLogGroup(graph *ir.Graph) bool {
	if graph == nil {
		return false
	}
	if len(graph.Resources.StatefulGroups) > 0 {
		return true
	}
	for _, cfg := range graph.Resources.LogConfigs {
		if cfg.Enabled {
			return true
		}
	}
	return false
}

func hasMetrics(graph *ir.Graph) bool {
	if graph == nil {
		return false
	}
	for _, cfg := range graph.Resources.MetricConfigs {
		if cfg.Enabled {
			return true
		}
	}
	for _, recipe := range graph.Resources.StatefulRecipes {
		if recipe.Metrics.Enabled {
			return true
		}
	}
	return false
}

func hasUsageBasedLineItems(lines []InfraLineItem) bool {
	for _, line := range lines {
		if !line.Estimated {
			return true
		}
	}
	return false
}

func termHoursFromScheme(scheme string) int {
	switch scheme {
	case PricingSchemeRI1yrStandardNoUpfront:
		return 8760
	case PricingSchemeRI3yrStandardNoUpfront, PricingSchemeRI3yrStandardAllUpfront:
		return 26280
	default:
		return 0
	}
}

func pricingSchemeSort(scheme string) int {
	switch scheme {
	case PricingSchemeOnDemand:
		return 0
	case PricingSchemeRI1yrStandardNoUpfront:
		return 1
	case PricingSchemeRI3yrStandardNoUpfront:
		return 2
	case PricingSchemeRI3yrStandardAllUpfront:
		return 3
	default:
		return 100
	}
}

func (catalog PricingCatalog) storageRate(kind, resourceType string) (StoragePricing, bool) {
	for _, rate := range catalog.StorageRates {
		if rate.Kind != kind {
			continue
		}
		if resourceType == "" || rate.ResourceType == "" || rate.ResourceType == resourceType {
			return rate, true
		}
	}
	return StoragePricing{}, false
}

func parseGiB(value string) float64 {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "")
	for _, suffix := range []string{"gib", "gb", "gi", "g"} {
		if strings.HasSuffix(value, suffix) {
			value = strings.TrimSuffix(value, suffix)
			break
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}
