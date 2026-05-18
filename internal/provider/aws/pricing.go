package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	skiffcost "github.com/s1liconcow/skiff/internal/cost"
	"github.com/s1liconcow/skiff/internal/ir"
)

const ec2PriceListURLPattern = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/%s/index.json"

type EC2PricingOptions struct {
	Region     string
	SourcePath string
	Machines   []ir.Machine
	Schemes    []skiffcost.PricingScheme
	HTTPClient *http.Client
}

func EC2PriceListURL(region string) string {
	return fmt.Sprintf(ec2PriceListURLPattern, strings.TrimSpace(region))
}

func DefaultCostMachines() []ir.Machine {
	return []ir.Machine{{Size: "small"}, {Size: "medium"}, {Size: "large"}}
}

func LoadEC2Pricing(ctx context.Context, opts EC2PricingOptions) (skiffcost.PricingCatalog, error) {
	if strings.TrimSpace(opts.Region) == "" {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS region is required for pricing")
	}
	if opts.SourcePath != "" {
		file, err := os.Open(opts.SourcePath)
		if err != nil {
			return skiffcost.PricingCatalog{}, fmt.Errorf("open AWS EC2 pricing file: %w", err)
		}
		defer file.Close()
		catalog, err := ParseEC2Pricing(file, opts)
		if err != nil {
			return skiffcost.PricingCatalog{}, err
		}
		catalog.Source = opts.SourcePath
		return catalog, nil
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	url := EC2PriceListURL(opts.Region)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("build AWS EC2 pricing request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("fetch AWS EC2 pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return skiffcost.PricingCatalog{}, fmt.Errorf("fetch AWS EC2 pricing: HTTP %d", resp.StatusCode)
	}
	catalog, err := ParseEC2Pricing(resp.Body, opts)
	if err != nil {
		return skiffcost.PricingCatalog{}, err
	}
	catalog.Source = url
	return catalog, nil
}

func ParseEC2Pricing(r io.Reader, opts EC2PricingOptions) (skiffcost.PricingCatalog, error) {
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS region is required for pricing")
	}
	schemes := opts.Schemes
	if len(schemes) == 0 {
		schemes = skiffcost.DefaultPricingSchemes()
	}
	targets := targetPricingMachines(opts.Machines)
	var doc ec2PriceListDocument
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&doc); err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("decode AWS EC2 pricing: %w", err)
	}
	selected := make(map[string]skiffcost.InstancePricing)
	for sku, product := range doc.Products {
		instanceType := product.Attributes["instanceType"]
		machineSize, ok := targets[instanceType]
		if !ok || !isLinuxSharedUsedInstance(product.Attributes) {
			continue
		}
		selected[sku] = skiffcost.InstancePricing{
			MachineSize:  machineSize,
			InstanceType: instanceType,
			VCPU:         parseInt(product.Attributes["vcpu"]),
			MemoryGB:     parseMemoryGiB(product.Attributes["memory"]),
		}
	}
	if len(selected) == 0 {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS EC2 pricing has no matching Linux shared-tenancy instance products")
	}
	items := make([]skiffcost.InstancePricing, 0, len(selected))
	for sku, item := range selected {
		item.Rates = ratesForSKU(doc.Terms, sku, schemes)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left.MachineSize == right.MachineSize {
			return left.InstanceType < right.InstanceType
		}
		return machineSortIndex(left.MachineSize) < machineSortIndex(right.MachineSize)
	})
	return skiffcost.PricingCatalog{
		SchemaVersion:   skiffcost.PricingCatalogSchemaVersion,
		Provider:        Name,
		Region:          region,
		Currency:        "USD",
		PublicationDate: doc.PublicationDate,
		Version:         doc.Version,
		Items:           items,
		StorageRates:    storageRates(doc),
	}, nil
}

func targetPricingMachines(machines []ir.Machine) map[string]string {
	if len(machines) == 0 {
		machines = DefaultCostMachines()
	}
	targets := make(map[string]string, len(machines))
	for _, machine := range machines {
		size := strings.TrimSpace(machine.Size)
		if size == "" {
			size = "small"
		}
		targets[instanceTypeForMachine(machine)] = size
	}
	return targets
}

func isLinuxSharedUsedInstance(attrs map[string]string) bool {
	return attrs["operatingSystem"] == "Linux" &&
		attrs["tenancy"] == "Shared" &&
		attrs["preInstalledSw"] == "NA" &&
		attrs["capacitystatus"] == "Used" &&
		attrs["operation"] == "RunInstances"
}

func ratesForSKU(terms ec2PriceTerms, sku string, schemes []skiffcost.PricingScheme) []skiffcost.PricingRate {
	rates := make([]skiffcost.PricingRate, 0, len(schemes))
	for _, scheme := range schemes {
		switch scheme.ID {
		case skiffcost.PricingSchemeOnDemand:
			if rate, ok := onDemandRate(terms.OnDemand[sku], scheme); ok {
				rates = append(rates, rate)
			}
		default:
			if rate, ok := reservedRate(terms.Reserved[sku], scheme); ok {
				rates = append(rates, rate)
			}
		}
	}
	return rates
}

func onDemandRate(terms map[string]ec2PriceTerm, scheme skiffcost.PricingScheme) (skiffcost.PricingRate, bool) {
	for _, term := range terms {
		hourly, ok := hourlyDimension(term.PriceDimensions)
		if !ok {
			continue
		}
		return skiffcost.PricingRate{
			Scheme:             scheme.ID,
			Summary:            scheme.Summary,
			Currency:           "USD",
			HourlyUSD:          hourly,
			EffectiveHourlyUSD: hourly,
		}, true
	}
	return skiffcost.PricingRate{}, false
}

func reservedRate(terms map[string]ec2PriceTerm, scheme skiffcost.PricingScheme) (skiffcost.PricingRate, bool) {
	for _, term := range terms {
		if term.TermAttributes["LeaseContractLength"] != scheme.LeaseContractLength ||
			term.TermAttributes["OfferingClass"] != scheme.OfferingClass ||
			term.TermAttributes["PurchaseOption"] != scheme.PurchaseOption {
			continue
		}
		hourly, _ := hourlyDimension(term.PriceDimensions)
		upfront := upfrontDimension(term.PriceDimensions)
		termHours := termHoursForLease(scheme.LeaseContractLength)
		if termHours == 0 {
			continue
		}
		return skiffcost.PricingRate{
			Scheme:             scheme.ID,
			Summary:            scheme.Summary,
			Currency:           "USD",
			HourlyUSD:          hourly,
			UpfrontUSD:         upfront,
			EffectiveHourlyUSD: hourly + upfront/float64(termHours),
			TermHours:          termHours,
		}, true
	}
	return skiffcost.PricingRate{}, false
}

func storageRates(doc ec2PriceListDocument) []skiffcost.StoragePricing {
	var rates []skiffcost.StoragePricing
	for sku, product := range doc.Products {
		if rate, ok := ebsVolumeStorageRate(doc.Terms.OnDemand[sku], product); ok {
			rates = append(rates, rate)
			continue
		}
		if rate, ok := ebsSnapshotStorageRate(doc.Terms.OnDemand[sku], product); ok {
			rates = append(rates, rate)
		}
	}
	sort.Slice(rates, func(i, j int) bool {
		if rates[i].Kind == rates[j].Kind {
			return rates[i].ResourceType < rates[j].ResourceType
		}
		return rates[i].Kind < rates[j].Kind
	})
	return rates
}

func ebsVolumeStorageRate(terms map[string]ec2PriceTerm, product ec2Product) (skiffcost.StoragePricing, bool) {
	if product.ProductFamily != "Storage" || product.Attributes["volumeApiName"] == "" {
		return skiffcost.StoragePricing{}, false
	}
	if !strings.Contains(product.Attributes["usagetype"], "VolumeUsage.") {
		return skiffcost.StoragePricing{}, false
	}
	price, unit, summary, ok := firstPriceDimension(terms, "GB-Mo")
	if !ok {
		return skiffcost.StoragePricing{}, false
	}
	return skiffcost.StoragePricing{
		Kind:         skiffcost.StorageKindEBSVolumeGBMonth,
		ResourceType: product.Attributes["volumeApiName"],
		Unit:         unit,
		UnitPriceUSD: price,
		Summary:      summary,
	}, true
}

func ebsSnapshotStorageRate(terms map[string]ec2PriceTerm, product ec2Product) (skiffcost.StoragePricing, bool) {
	if product.ProductFamily != "Storage Snapshot" || product.Attributes["locationType"] != "AWS Region" {
		return skiffcost.StoragePricing{}, false
	}
	if product.Attributes["usagetype"] == "" || strings.Contains(product.Attributes["usagetype"], "UnderBilling") || strings.Contains(product.Attributes["usagetype"], "outposts") {
		return skiffcost.StoragePricing{}, false
	}
	if !strings.Contains(product.Attributes["usagetype"], "SnapshotUsage") {
		return skiffcost.StoragePricing{}, false
	}
	price, unit, summary, ok := firstPriceDimension(terms, "GB-Mo")
	if !ok {
		return skiffcost.StoragePricing{}, false
	}
	return skiffcost.StoragePricing{
		Kind:         skiffcost.StorageKindEBSSnapshotGBMonth,
		Unit:         unit,
		UnitPriceUSD: price,
		Summary:      summary,
	}, true
}

func firstPriceDimension(terms map[string]ec2PriceTerm, preferredUnit string) (float64, string, string, bool) {
	for _, term := range terms {
		for _, dimension := range term.PriceDimensions {
			if preferredUnit != "" && dimension.Unit != preferredUnit {
				continue
			}
			return parsePrice(dimension.PricePerUnit["USD"]), dimension.Unit, dimension.Description, true
		}
	}
	return 0, "", "", false
}

func hourlyDimension(dimensions map[string]ec2PriceDimension) (float64, bool) {
	for _, dimension := range dimensions {
		if dimension.Unit != "Hrs" {
			continue
		}
		return parsePrice(dimension.PricePerUnit["USD"]), true
	}
	return 0, false
}

func upfrontDimension(dimensions map[string]ec2PriceDimension) float64 {
	for _, dimension := range dimensions {
		if dimension.Unit != "Quantity" {
			continue
		}
		return parsePrice(dimension.PricePerUnit["USD"])
	}
	return 0
}

func termHoursForLease(lease string) int {
	switch lease {
	case skiffcost.PricingLeaseContractLength1yr:
		return 8760
	case skiffcost.PricingLeaseContractLength3yr:
		return 26280
	default:
		return 0
	}
}

func parsePrice(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func parseMemoryGiB(value string) float64 {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64)
	if err != nil {
		return 0
	}
	return parsed
}

func machineSortIndex(size string) int {
	switch size {
	case "small":
		return 0
	case "medium":
		return 1
	case "large":
		return 2
	default:
		return 100
	}
}

type ec2PriceListDocument struct {
	PublicationDate string                `json:"publicationDate"`
	Version         string                `json:"version"`
	Products        map[string]ec2Product `json:"products"`
	Terms           ec2PriceTerms         `json:"terms"`
}

type ec2Product struct {
	ProductFamily string            `json:"productFamily"`
	Attributes    map[string]string `json:"attributes"`
}

type ec2PriceTerms struct {
	OnDemand map[string]map[string]ec2PriceTerm `json:"OnDemand"`
	Reserved map[string]map[string]ec2PriceTerm `json:"Reserved"`
}

type ec2PriceTerm struct {
	PriceDimensions map[string]ec2PriceDimension `json:"priceDimensions"`
	TermAttributes  map[string]string            `json:"termAttributes"`
}

type ec2PriceDimension struct {
	Unit         string            `json:"unit"`
	Description  string            `json:"description"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
}
