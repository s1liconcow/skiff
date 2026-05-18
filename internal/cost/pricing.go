package cost

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

func DefaultPricingSchemes() []PricingScheme {
	return []PricingScheme{
		{ID: PricingSchemeOnDemand, Summary: "On-Demand"},
		{
			ID:                  PricingSchemeRI1yrStandardNoUpfront,
			Summary:             "1yr Standard RI No Upfront",
			LeaseContractLength: PricingLeaseContractLength1yr,
			OfferingClass:       PricingOfferingClassStandard,
			PurchaseOption:      PricingPurchaseOptionNoUpfront,
		},
		{
			ID:                  PricingSchemeRI3yrStandardNoUpfront,
			Summary:             "3yr Standard RI No Upfront",
			LeaseContractLength: PricingLeaseContractLength3yr,
			OfferingClass:       PricingOfferingClassStandard,
			PurchaseOption:      PricingPurchaseOptionNoUpfront,
		},
		{
			ID:                  PricingSchemeRI3yrStandardAllUpfront,
			Summary:             "3yr Standard RI All Upfront",
			LeaseContractLength: PricingLeaseContractLength3yr,
			OfferingClass:       PricingOfferingClassStandard,
			PurchaseOption:      PricingPurchaseOptionAllUpfront,
		},
	}
}

func ParsePricingScheme(value string) (PricingScheme, error) {
	normalized := normalizePricingSchemeID(value)
	for _, scheme := range DefaultPricingSchemes() {
		if scheme.ID == normalized {
			return scheme, nil
		}
	}
	return PricingScheme{}, fmt.Errorf("unsupported pricing scheme %q", value)
}

func AnalyzeWithPricing(input Input, catalog PricingCatalog, opts PricingOptions) (Result, error) {
	result := Analyze(input)
	estimate, err := EstimatePricing(input, catalog, opts)
	if err != nil {
		return result, err
	}
	result.Pricing = &estimate
	result.Recommendations = enrichPricingImpacts(input, catalog, opts, result.Recommendations)
	result.Limitations = pricedLimitations(input)
	return result, nil
}

func EstimatePricing(input Input, catalog PricingCatalog, opts PricingOptions) (PricingEstimate, error) {
	shape := normalizeShape(input.Shape)
	if opts.MonthlyHours <= 0 {
		opts.MonthlyHours = DefaultMonthlyHours
	}
	item, ok := catalog.itemForMachineSize(shape.MachineSize)
	if !ok {
		item, ok = catalog.itemForInstanceType(shape.MachineSize)
	}
	if !ok {
		return PricingEstimate{}, fmt.Errorf("pricing catalog has no AWS instance rate for machine size %q", shape.MachineSize)
	}
	if len(item.Rates) == 0 {
		return PricingEstimate{}, fmt.Errorf("pricing catalog has no rates for instance type %q", item.InstanceType)
	}
	estimate := PricingEstimate{
		Provider:        catalog.Provider,
		Region:          catalog.Region,
		Currency:        firstNonEmpty(catalog.Currency, "USD"),
		Source:          catalog.Source,
		PublicationDate: catalog.PublicationDate,
		Version:         catalog.Version,
		MachineSize:     firstNonEmpty(item.MachineSize, shape.MachineSize),
		InstanceType:    item.InstanceType,
		VCPU:            item.VCPU,
		MemoryGB:        item.MemoryGB,
		MinReplicas:     shape.MinReplicas,
		MaxReplicas:     shape.MaxReplicas,
		MonthlyHours:    opts.MonthlyHours,
	}
	for _, rate := range item.Rates {
		effectiveHourly := rate.EffectiveHourlyUSD
		if effectiveHourly == 0 {
			effectiveHourly = rate.HourlyUSD
		}
		scheme := PricingSchemeEstimate{
			Scheme:             rate.Scheme,
			Summary:            rate.Summary,
			HourlyUSD:          roundMoney(rate.HourlyUSD),
			UpfrontUSD:         roundMoney(rate.UpfrontUSD),
			EffectiveHourlyUSD: roundMoney(effectiveHourly),
			TermHours:          rate.TermHours,
			MinHourlyUSD:       roundMoney(effectiveHourly * float64(shape.MinReplicas)),
			MaxHourlyUSD:       roundMoney(effectiveHourly * float64(shape.MaxReplicas)),
			MinMonthlyUSD:      roundMoney(effectiveHourly * float64(shape.MinReplicas) * opts.MonthlyHours),
			MaxMonthlyUSD:      roundMoney(effectiveHourly * float64(shape.MaxReplicas) * opts.MonthlyHours),
			MinAnnualUSD:       roundMoney(effectiveHourly * float64(shape.MinReplicas) * 8760),
			MaxAnnualUSD:       roundMoney(effectiveHourly * float64(shape.MaxReplicas) * 8760),
			MinUpfrontUSD:      roundMoney(rate.UpfrontUSD * float64(shape.MinReplicas)),
			MaxUpfrontUSD:      roundMoney(rate.UpfrontUSD * float64(shape.MaxReplicas)),
		}
		if rate.TermHours > 0 {
			scheme.MinTermUSD = roundMoney((rate.HourlyUSD*float64(rate.TermHours) + rate.UpfrontUSD) * float64(shape.MinReplicas))
			scheme.MaxTermUSD = roundMoney((rate.HourlyUSD*float64(rate.TermHours) + rate.UpfrontUSD) * float64(shape.MaxReplicas))
		}
		estimate.Schemes = append(estimate.Schemes, scheme)
	}
	return estimate, nil
}

func PricingSchemeIDs(schemes []PricingScheme) []string {
	if len(schemes) == 0 {
		return nil
	}
	out := make([]string, 0, len(schemes))
	for _, scheme := range schemes {
		out = append(out, scheme.ID)
	}
	return out
}

func PricingSchemeForID(id string) (PricingScheme, bool) {
	normalized := normalizePricingSchemeID(id)
	for _, scheme := range DefaultPricingSchemes() {
		if scheme.ID == normalized {
			return scheme, true
		}
	}
	return PricingScheme{}, false
}

func (catalog PricingCatalog) itemForMachineSize(machineSize string) (InstancePricing, bool) {
	machineSize = strings.TrimSpace(machineSize)
	for _, item := range catalog.Items {
		if item.MachineSize == machineSize {
			return item, true
		}
	}
	return InstancePricing{}, false
}

func (catalog PricingCatalog) itemForInstanceType(instanceType string) (InstancePricing, bool) {
	instanceType = strings.TrimSpace(instanceType)
	for _, item := range catalog.Items {
		if item.InstanceType == instanceType {
			return item, true
		}
	}
	return InstancePricing{}, false
}

func enrichPricingImpacts(input Input, catalog PricingCatalog, opts PricingOptions, recs []Recommendation) []Recommendation {
	if len(recs) == 0 {
		return recs
	}
	shape := normalizeShape(input.Shape)
	current, ok := catalog.itemForMachineSize(shape.MachineSize)
	if !ok {
		return recs
	}
	out := append([]Recommendation(nil), recs...)
	for i := range out {
		switch out[i].ID {
		case "cost.shape.downsize":
			idx := indexOfShape(shape.MachineSize)
			if idx <= 0 {
				continue
			}
			next, ok := catalog.itemForMachineSize(shapeCatalog[idx-1].Name)
			if !ok {
				continue
			}
			if impact := formatPricingDelta(current, next, shape.MinReplicas, shape.MinReplicas, opts.MonthlyHours); impact != "" {
				out[i].EstimatedImpact = impact
			}
		case "cost.replicas.reduce_min":
			target := warmCapacityFromEvidence(input.Signals)
			if target <= 0 || target >= shape.MinReplicas {
				continue
			}
			if impact := formatPricingDelta(current, current, shape.MinReplicas, target, opts.MonthlyHours); impact != "" {
				out[i].EstimatedImpact = impact
			}
		}
	}
	return out
}

func formatPricingDelta(from, to InstancePricing, fromReplicas, toReplicas int, monthlyHours float64) string {
	if monthlyHours <= 0 {
		monthlyHours = DefaultMonthlyHours
	}
	if fromReplicas <= 0 || toReplicas <= 0 {
		return ""
	}
	fromRates := ratesByScheme(from.Rates)
	toRates := ratesByScheme(to.Rates)
	var parts []string
	for _, schemeID := range []string{PricingSchemeOnDemand, PricingSchemeRI1yrStandardNoUpfront, PricingSchemeRI3yrStandardAllUpfront} {
		fromRate, fromOK := fromRates[schemeID]
		toRate, toOK := toRates[schemeID]
		if !fromOK || !toOK {
			continue
		}
		fromMonthly := effectiveHourly(fromRate) * float64(fromReplicas) * monthlyHours
		toMonthly := effectiveHourly(toRate) * float64(toReplicas) * monthlyHours
		if toMonthly >= fromMonthly {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s saves about %s/month at min replicas", schemeID, formatUSD(fromMonthly-toMonthly)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func ratesByScheme(rates []PricingRate) map[string]PricingRate {
	out := make(map[string]PricingRate, len(rates))
	for _, rate := range rates {
		out[rate.Scheme] = rate
	}
	return out
}

func warmCapacityFromEvidence(signals ObservedSignals) int {
	if signals.WarmCapacity == nil {
		return 0
	}
	return *signals.WarmCapacity
}

func pricedLimitations(input Input) []string {
	limits := []string{
		"pricing estimates include EC2 instance compute, RDS instance compute, and baseline EBS/RDS storage when present; they exclude load balancers, NAT, data transfer, CloudWatch usage, taxes, credits, Savings Plans, and private discounts",
		"RI estimates are effective hourly equivalents for matching regional Standard RI terms; actual billing can differ with existing reservations, size flexibility, scope, engine, and account discounts",
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

func normalizePricingSchemeID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "", "ondemand", "on_demand":
		return PricingSchemeOnDemand
	case "ri_1yr_standard_no_upfront", "reserved_1yr_standard_no_upfront":
		return PricingSchemeRI1yrStandardNoUpfront
	case "ri_3yr_standard_no_upfront", "reserved_3yr_standard_no_upfront":
		return PricingSchemeRI3yrStandardNoUpfront
	case "ri_3yr_standard_all_upfront", "reserved_3yr_standard_all_upfront":
		return PricingSchemeRI3yrStandardAllUpfront
	default:
		return value
	}
}

func effectiveHourly(rate PricingRate) float64 {
	if rate.EffectiveHourlyUSD != 0 {
		return rate.EffectiveHourlyUSD
	}
	return rate.HourlyUSD
}

func roundMoney(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*1000000) / 1000000
}

func formatUSD(value float64) string {
	return "$" + strconvFormatMoney(value)
}

func strconvFormatMoney(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ValidatePricingCatalog(catalog PricingCatalog) error {
	if strings.TrimSpace(catalog.Provider) == "" {
		return errors.New("pricing catalog provider is required")
	}
	if strings.TrimSpace(catalog.Region) == "" {
		return errors.New("pricing catalog region is required")
	}
	return nil
}
