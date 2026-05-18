package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadPricingCatalogFile(path string) (PricingCatalog, error) {
	file, err := os.Open(path)
	if err != nil {
		return PricingCatalog{}, err
	}
	defer file.Close()
	var catalog PricingCatalog
	if err := json.NewDecoder(file).Decode(&catalog); err != nil {
		return PricingCatalog{}, fmt.Errorf("decode pricing config %s: %w", path, err)
	}
	if catalog.SchemaVersion == "" {
		catalog.SchemaVersion = PricingCatalogSchemaVersion
	}
	if catalog.Currency == "" {
		catalog.Currency = "USD"
	}
	if err := ValidatePricingCatalog(catalog); err != nil {
		return PricingCatalog{}, fmt.Errorf("pricing config %s: %w", path, err)
	}
	return catalog, nil
}

func WritePricingCatalogFile(path string, catalog PricingCatalog) error {
	catalog.SchemaVersion = firstNonEmpty(catalog.SchemaVersion, PricingCatalogSchemaVersion)
	if catalog.Currency == "" {
		catalog.Currency = "USD"
	}
	if err := ValidatePricingCatalog(catalog); err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create pricing config directory: %w", err)
		}
	}
	body, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pricing config: %w", err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write pricing config %s: %w", path, err)
	}
	return nil
}

func FilterPricingCatalogSchemes(catalog PricingCatalog, schemes []PricingScheme) PricingCatalog {
	if len(schemes) == 0 {
		return catalog
	}
	keep := make(map[string]struct{}, len(schemes))
	for _, scheme := range schemes {
		if strings.TrimSpace(scheme.ID) != "" {
			keep[scheme.ID] = struct{}{}
		}
	}
	if len(keep) == 0 {
		return catalog
	}
	out := catalog
	out.Items = make([]InstancePricing, 0, len(catalog.Items))
	for _, item := range catalog.Items {
		next := item
		next.Rates = nil
		for _, rate := range item.Rates {
			if _, ok := keep[rate.Scheme]; ok {
				next.Rates = append(next.Rates, rate)
			}
		}
		if len(next.Rates) > 0 {
			out.Items = append(out.Items, next)
		}
	}
	out.DatabaseItems = make([]DatabaseInstancePricing, 0, len(catalog.DatabaseItems))
	for _, item := range catalog.DatabaseItems {
		next := item
		next.Rates = nil
		for _, rate := range item.Rates {
			if _, ok := keep[rate.Scheme]; ok {
				next.Rates = append(next.Rates, rate)
			}
		}
		if len(next.Rates) > 0 {
			out.DatabaseItems = append(out.DatabaseItems, next)
		}
	}
	return out
}

func MergePricingCatalogs(catalogs ...PricingCatalog) PricingCatalog {
	var out PricingCatalog
	var sources []string
	var versions []string
	for _, catalog := range catalogs {
		if strings.TrimSpace(catalog.Provider) == "" {
			continue
		}
		if out.Provider == "" {
			out.Provider = catalog.Provider
			out.Region = catalog.Region
			out.Currency = catalog.Currency
			out.SchemaVersion = firstNonEmpty(catalog.SchemaVersion, PricingCatalogSchemaVersion)
		}
		if catalog.PublicationDate > out.PublicationDate {
			out.PublicationDate = catalog.PublicationDate
		}
		if catalog.Source != "" {
			sources = append(sources, catalog.Source)
		}
		if catalog.Version != "" {
			versions = append(versions, catalog.Version)
		}
		out.Items = append(out.Items, catalog.Items...)
		out.DatabaseItems = append(out.DatabaseItems, catalog.DatabaseItems...)
		out.StorageRates = append(out.StorageRates, catalog.StorageRates...)
	}
	if out.Currency == "" {
		out.Currency = "USD"
	}
	out.Source = strings.Join(uniqueStrings(sources), ", ")
	out.Version = strings.Join(uniqueStrings(versions), ",")
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
