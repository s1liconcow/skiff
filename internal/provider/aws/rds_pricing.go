package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	skiffcost "github.com/s1liconcow/skiff/internal/cost"
)

const rdsPriceListURLPattern = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonRDS/current/%s/index.json"

type RDSDatabaseTarget struct {
	Engine           string
	Size             string
	InstanceClass    string
	DeploymentOption string
}

type RDSPricingOptions struct {
	Region     string
	SourcePath string
	Databases  []RDSDatabaseTarget
	Schemes    []skiffcost.PricingScheme
	HTTPClient *http.Client
}

func RDSPriceListURL(region string) string {
	return fmt.Sprintf(rdsPriceListURLPattern, strings.TrimSpace(region))
}

func DefaultCostDatabases() []RDSDatabaseTarget {
	var out []RDSDatabaseTarget
	for _, engine := range []string{"postgres", "mysql", "mariadb"} {
		for _, size := range []string{"small", "medium", "large"} {
			out = append(out, RDSDatabaseTarget{Engine: engine, Size: size, InstanceClass: databaseInstanceClass(size), DeploymentOption: "Single-AZ"})
		}
	}
	return out
}

func LoadRDSPricing(ctx context.Context, opts RDSPricingOptions) (skiffcost.PricingCatalog, error) {
	if strings.TrimSpace(opts.Region) == "" {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS region is required for RDS pricing")
	}
	if opts.SourcePath != "" {
		file, err := os.Open(opts.SourcePath)
		if err != nil {
			return skiffcost.PricingCatalog{}, fmt.Errorf("open AWS RDS pricing file: %w", err)
		}
		defer file.Close()
		catalog, err := ParseRDSPricing(file, opts)
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
	url := RDSPriceListURL(opts.Region)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("build AWS RDS pricing request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("fetch AWS RDS pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return skiffcost.PricingCatalog{}, fmt.Errorf("fetch AWS RDS pricing: HTTP %d", resp.StatusCode)
	}
	catalog, err := ParseRDSPricing(resp.Body, opts)
	if err != nil {
		return skiffcost.PricingCatalog{}, err
	}
	catalog.Source = url
	return catalog, nil
}

func ParseRDSPricing(r io.Reader, opts RDSPricingOptions) (skiffcost.PricingCatalog, error) {
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS region is required for RDS pricing")
	}
	schemes := opts.Schemes
	if len(schemes) == 0 {
		schemes = skiffcost.DefaultPricingSchemes()
	}
	targets := targetRDSDatabases(opts.Databases)
	var doc ec2PriceListDocument
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&doc); err != nil {
		return skiffcost.PricingCatalog{}, fmt.Errorf("decode AWS RDS pricing: %w", err)
	}
	selected := make(map[string]skiffcost.DatabaseInstancePricing)
	for sku, product := range doc.Products {
		if product.ProductFamily != "Database Instance" || !isRDSRegionProduct(product.Attributes) {
			continue
		}
		target, ok := targets[rdsTargetKey(product.Attributes["databaseEngine"], product.Attributes["instanceType"], product.Attributes["deploymentOption"])]
		if !ok || !isRDSLicenseCompatible(product.Attributes) {
			continue
		}
		selected[sku] = skiffcost.DatabaseInstancePricing{
			Engine:           target.Engine,
			Size:             target.Size,
			InstanceClass:    product.Attributes["instanceType"],
			DeploymentOption: product.Attributes["deploymentOption"],
			VCPU:             parseInt(product.Attributes["vcpu"]),
			MemoryGB:         parseMemoryGiB(product.Attributes["memory"]),
		}
	}
	if len(selected) == 0 {
		return skiffcost.PricingCatalog{}, fmt.Errorf("AWS RDS pricing has no matching database instance products")
	}
	items := make([]skiffcost.DatabaseInstancePricing, 0, len(selected))
	for sku, item := range selected {
		item.Rates = ratesForSKU(doc.Terms, sku, schemes)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left.Engine != right.Engine {
			return left.Engine < right.Engine
		}
		if left.Size != right.Size {
			return machineSortIndex(left.Size) < machineSortIndex(right.Size)
		}
		return left.InstanceClass < right.InstanceClass
	})
	return skiffcost.PricingCatalog{
		SchemaVersion:   skiffcost.PricingCatalogSchemaVersion,
		Provider:        Name,
		Region:          region,
		Currency:        "USD",
		PublicationDate: doc.PublicationDate,
		Version:         doc.Version,
		DatabaseItems:   items,
		StorageRates:    rdsStorageRates(doc, targets),
	}, nil
}

func targetRDSDatabases(databases []RDSDatabaseTarget) map[string]RDSDatabaseTarget {
	if len(databases) == 0 {
		databases = DefaultCostDatabases()
	}
	out := make(map[string]RDSDatabaseTarget, len(databases))
	for _, db := range databases {
		engine := rdsEngineID(db.Engine)
		if engine == "" {
			continue
		}
		size := strings.TrimSpace(db.Size)
		if size == "" {
			size = "small"
		}
		instanceClass := strings.TrimSpace(db.InstanceClass)
		if instanceClass == "" {
			instanceClass = databaseInstanceClass(size)
		}
		deployment := strings.TrimSpace(db.DeploymentOption)
		if deployment == "" {
			deployment = "Single-AZ"
		}
		target := RDSDatabaseTarget{Engine: engine, Size: size, InstanceClass: instanceClass, DeploymentOption: deployment}
		out[rdsTargetKey(rdsEngineDisplay(engine), instanceClass, deployment)] = target
	}
	return out
}

func isRDSRegionProduct(attrs map[string]string) bool {
	return attrs["locationType"] == "AWS Region"
}

func isRDSLicenseCompatible(attrs map[string]string) bool {
	license := strings.TrimSpace(attrs["licenseModel"])
	return license == "" || license == "No license required"
}

func rdsStorageRates(doc ec2PriceListDocument, targets map[string]RDSDatabaseTarget) []skiffcost.StoragePricing {
	targetEngines := map[string]string{"Any": ""}
	for _, target := range targets {
		targetEngines[rdsEngineDisplay(target.Engine)] = target.Engine
	}
	seen := map[string]struct{}{}
	var rates []skiffcost.StoragePricing
	for sku, product := range doc.Products {
		attrs := product.Attributes
		if !isRDSRegionProduct(attrs) {
			continue
		}
		engine, engineOK := targetEngines[attrs["databaseEngine"]]
		if !engineOK {
			continue
		}
		deployment := strings.TrimSpace(attrs["deploymentOption"])
		if deployment == "" {
			deployment = "Single-AZ"
		}
		if deployment != "Single-AZ" {
			continue
		}
		switch product.ProductFamily {
		case "Database Storage":
			volumeType := normalizeRDSVolumeType(attrs)
			if volumeType == "" {
				continue
			}
			if rate, ok := rdsStorageRateFromSKU(doc.Terms.OnDemand[sku], skiffcost.StorageKindRDSVolumeGBMonth, engine, volumeType, deployment); ok {
				key := rate.Kind + "|" + rate.Engine + "|" + rate.ResourceType + "|" + rate.DeploymentOption
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				rates = append(rates, rate)
			}
		case "Storage Snapshot":
			if rate, ok := rdsStorageRateFromSKU(doc.Terms.OnDemand[sku], skiffcost.StorageKindRDSBackupGBMonth, engine, "backup", deployment); ok {
				key := rate.Kind + "|" + rate.Engine + "|" + rate.ResourceType + "|" + rate.DeploymentOption
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				rates = append(rates, rate)
			}
		}
	}
	sort.Slice(rates, func(i, j int) bool {
		if rates[i].Kind != rates[j].Kind {
			return rates[i].Kind < rates[j].Kind
		}
		if rates[i].Engine != rates[j].Engine {
			return rates[i].Engine < rates[j].Engine
		}
		return rates[i].ResourceType < rates[j].ResourceType
	})
	return rates
}

func rdsStorageRateFromSKU(terms map[string]ec2PriceTerm, kind, engine, resourceType, deployment string) (skiffcost.StoragePricing, bool) {
	price, unit, summary, ok := firstPriceDimension(terms, "GB-Mo")
	if !ok {
		return skiffcost.StoragePricing{}, false
	}
	return skiffcost.StoragePricing{
		Kind:             kind,
		Engine:           engine,
		ResourceType:     resourceType,
		DeploymentOption: deployment,
		Unit:             unit,
		UnitPriceUSD:     price,
		Summary:          summary,
	}, true
}

func normalizeRDSVolumeType(attrs map[string]string) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(attrs["volumeName"], attrs["volumeType"], attrs["usagetype"])))
	switch {
	case strings.Contains(value, "gp3"):
		return "gp3"
	case strings.Contains(value, "gp2") || strings.Contains(value, "general purpose"):
		return "gp2"
	case strings.Contains(value, "io2"):
		return "io2"
	case strings.Contains(value, "piops") || strings.Contains(value, "provisioned iops"):
		return "io1"
	case strings.Contains(value, "magnetic") || strings.Contains(value, "storageusage"):
		return "magnetic"
	default:
		return ""
	}
}

func rdsTargetKey(engine, instanceClass, deployment string) string {
	return strings.ToLower(strings.TrimSpace(engine)) + "|" + strings.ToLower(strings.TrimSpace(instanceClass)) + "|" + strings.ToLower(strings.TrimSpace(deployment))
}

func rdsEngineDisplay(engine string) string {
	switch rdsEngineID(engine) {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "mariadb":
		return "MariaDB"
	default:
		return strings.TrimSpace(engine)
	}
}

func rdsEngineID(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres", "postgresql":
		return "postgres"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}
