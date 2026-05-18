package aws

import (
	"strings"
	"testing"

	skiffcost "github.com/s1liconcow/skiff/internal/cost"
)

func TestParseRDSPricingExtractsPostgresInstanceAndStorage(t *testing.T) {
	catalog, err := ParseRDSPricing(strings.NewReader(rdsPricingFixture), RDSPricingOptions{
		Region: "us-east-1",
		Databases: []RDSDatabaseTarget{
			{Engine: "postgres", Size: "small", InstanceClass: "db.t4g.micro", DeploymentOption: "Single-AZ"},
		},
		Schemes: []skiffcost.PricingScheme{
			mustPricingScheme(t, "on-demand"),
			mustPricingScheme(t, "ri-3yr-standard-all-upfront"),
		},
	})
	if err != nil {
		t.Fatalf("ParseRDSPricing: %v", err)
	}
	if catalog.Provider != Name || catalog.Region != "us-east-1" || catalog.PublicationDate != "2026-05-14T21:07:47Z" {
		t.Fatalf("unexpected catalog metadata: %+v", catalog)
	}
	if len(catalog.DatabaseItems) != 1 {
		t.Fatalf("database items = %+v", catalog.DatabaseItems)
	}
	item := catalog.DatabaseItems[0]
	if item.Engine != "postgres" || item.Size != "small" || item.InstanceClass != "db.t4g.micro" || item.VCPU != 2 || item.MemoryGB != 1 {
		t.Fatalf("unexpected database item: %+v", item)
	}
	if len(item.Rates) != 2 || item.Rates[0].EffectiveHourlyUSD != 0.016 || item.Rates[1].UpfrontUSD != 199 {
		t.Fatalf("unexpected database rates: %+v", item.Rates)
	}
	if len(catalog.StorageRates) != 2 {
		t.Fatalf("storage rates = %+v", catalog.StorageRates)
	}
}

const rdsPricingFixture = `{
  "publicationDate": "2026-05-14T21:07:47Z",
  "version": "20260514210747",
  "products": {
    "RDS_PG_SMALL": {
      "productFamily": "Database Instance",
      "attributes": {
        "locationType": "AWS Region",
        "instanceType": "db.t4g.micro",
        "vcpu": "2",
        "memory": "1 GiB",
        "databaseEngine": "PostgreSQL",
        "licenseModel": "No license required",
        "deploymentOption": "Single-AZ"
      }
    },
    "RDS_PG_GP3": {
      "productFamily": "Database Storage",
      "attributes": {
        "locationType": "AWS Region",
        "volumeType": "General Purpose-GP3",
        "databaseEngine": "PostgreSQL",
        "deploymentOption": "Single-AZ"
      }
    },
    "RDS_PG_BACKUP": {
      "productFamily": "Storage Snapshot",
      "attributes": {
        "locationType": "AWS Region",
        "databaseEngine": "PostgreSQL",
        "deploymentOption": "Single-AZ"
      }
    }
  },
  "terms": {
    "OnDemand": {
      "RDS_PG_SMALL": {
        "RDS_PG_SMALL.OD": {
          "priceDimensions": {
            "RDS_PG_SMALL.OD.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0160000000"}}
          }
        }
      },
      "RDS_PG_GP3": {
        "RDS_PG_GP3.OD": {
          "priceDimensions": {
            "RDS_PG_GP3.OD.GBMO": {"unit": "GB-Mo", "description": "$0.115 per GB-month of provisioned GP3 storage running PostgreSQL", "pricePerUnit": {"USD": "0.1150000000"}}
          }
        }
      },
      "RDS_PG_BACKUP": {
        "RDS_PG_BACKUP.OD": {
          "priceDimensions": {
            "RDS_PG_BACKUP.OD.GBMO": {"unit": "GB-Mo", "description": "$0.095 per additional GB-month of backup storage exceeding free allocation running PostgreSQL", "pricePerUnit": {"USD": "0.0950000000"}}
          }
        }
      }
    },
    "Reserved": {
      "RDS_PG_SMALL": {
        "RDS_PG_SMALL.RI3ALL": {
          "termAttributes": {
            "LeaseContractLength": "3yr",
            "OfferingClass": "standard",
            "PurchaseOption": "All Upfront"
          },
          "priceDimensions": {
            "RDS_PG_SMALL.RI3ALL.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0000000000"}},
            "RDS_PG_SMALL.RI3ALL.QTY": {"unit": "Quantity", "pricePerUnit": {"USD": "199"}}
          }
        }
      }
    }
  }
}`
