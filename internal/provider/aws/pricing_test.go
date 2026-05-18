package aws

import (
	"context"
	"strings"
	"testing"

	skiffcost "github.com/s1liconcow/skiff/internal/cost"
	"github.com/s1liconcow/skiff/internal/ir"
)

func TestParseEC2PricingExtractsOnDemandAndRI(t *testing.T) {
	catalog, err := ParseEC2Pricing(strings.NewReader(ec2PricingFixture), EC2PricingOptions{
		Region:   "us-east-1",
		Machines: []ir.Machine{{Size: "small"}, {Size: "medium"}},
		Schemes: []skiffcost.PricingScheme{
			mustPricingScheme(t, "on-demand"),
			mustPricingScheme(t, "ri-3yr-standard-all-upfront"),
		},
	})
	if err != nil {
		t.Fatalf("ParseEC2Pricing: %v", err)
	}
	if catalog.Provider != Name || catalog.Region != "us-east-1" || catalog.PublicationDate != "2026-05-14T21:07:47Z" {
		t.Fatalf("unexpected catalog metadata: %+v", catalog)
	}
	if len(catalog.Items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(catalog.Items), catalog.Items)
	}
	if len(catalog.StorageRates) != 2 {
		t.Fatalf("storage rates = %+v", catalog.StorageRates)
	}
	medium := catalog.Items[1]
	if medium.MachineSize != "medium" || medium.InstanceType != "t3.medium" || medium.VCPU != 2 || medium.MemoryGB != 4 {
		t.Fatalf("unexpected medium item: %+v", medium)
	}
	if len(medium.Rates) != 2 {
		t.Fatalf("medium rates = %+v", medium.Rates)
	}
	if got, want := medium.Rates[0].EffectiveHourlyUSD, 0.0416; got != want {
		t.Fatalf("on-demand effective hourly = %v, want %v", got, want)
	}
	if got, want := medium.Rates[1].UpfrontUSD, 411.0; got != want {
		t.Fatalf("RI upfront = %v, want %v", got, want)
	}
}

func TestLoadEC2PricingRequiresRegion(t *testing.T) {
	_, err := LoadEC2Pricing(context.Background(), EC2PricingOptions{})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("expected region error, got %v", err)
	}
}

func mustPricingScheme(t *testing.T, value string) skiffcost.PricingScheme {
	t.Helper()
	scheme, err := skiffcost.ParsePricingScheme(value)
	if err != nil {
		t.Fatal(err)
	}
	return scheme
}

const ec2PricingFixture = `{
  "publicationDate": "2026-05-14T21:07:47Z",
  "version": "20260514210747",
  "products": {
    "SKU_SMALL": {
      "productFamily": "Compute Instance",
      "attributes": {
        "instanceType": "t3.small",
        "operatingSystem": "Linux",
        "tenancy": "Shared",
        "preInstalledSw": "NA",
        "capacitystatus": "Used",
        "operation": "RunInstances",
        "vcpu": "2",
        "memory": "2 GiB"
      }
    },
    "SKU_MEDIUM": {
      "productFamily": "Compute Instance",
      "attributes": {
        "instanceType": "t3.medium",
        "operatingSystem": "Linux",
        "tenancy": "Shared",
        "preInstalledSw": "NA",
        "capacitystatus": "Used",
        "operation": "RunInstances",
        "vcpu": "2",
        "memory": "4 GiB"
      }
    },
    "SKU_GP3": {
      "productFamily": "Storage",
      "attributes": {
        "volumeApiName": "gp3",
        "usagetype": "EBS:VolumeUsage.gp3",
        "locationType": "AWS Region"
      }
    },
    "SKU_SNAPSHOT": {
      "productFamily": "Storage Snapshot",
      "attributes": {
        "usagetype": "EBS:SnapshotUsage",
        "locationType": "AWS Region"
      }
    }
  },
  "terms": {
    "OnDemand": {
      "SKU_SMALL": {
        "SKU_SMALL.OD": {
          "priceDimensions": {
            "SKU_SMALL.OD.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0208000000"}}
          }
        }
      },
      "SKU_MEDIUM": {
        "SKU_MEDIUM.OD": {
          "priceDimensions": {
            "SKU_MEDIUM.OD.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0416000000"}}
          }
        }
      },
      "SKU_GP3": {
        "SKU_GP3.OD": {
          "priceDimensions": {
            "SKU_GP3.OD.GBMO": {"unit": "GB-Mo", "description": "$0.08 per GB-month of General Purpose (gp3) provisioned storage", "pricePerUnit": {"USD": "0.0800000000"}}
          }
        }
      },
      "SKU_SNAPSHOT": {
        "SKU_SNAPSHOT.OD": {
          "priceDimensions": {
            "SKU_SNAPSHOT.OD.GBMO": {"unit": "GB-Mo", "description": "$0.05 per GB-Month of snapshot data stored", "pricePerUnit": {"USD": "0.0500000000"}}
          }
        }
      }
    },
    "Reserved": {
      "SKU_SMALL": {
        "SKU_SMALL.RI3ALL": {
          "termAttributes": {
            "LeaseContractLength": "3yr",
            "OfferingClass": "standard",
            "PurchaseOption": "All Upfront"
          },
          "priceDimensions": {
            "SKU_SMALL.RI3ALL.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0000000000"}},
            "SKU_SMALL.RI3ALL.QTY": {"unit": "Quantity", "pricePerUnit": {"USD": "206"}}
          }
        }
      },
      "SKU_MEDIUM": {
        "SKU_MEDIUM.RI3ALL": {
          "termAttributes": {
            "LeaseContractLength": "3yr",
            "OfferingClass": "standard",
            "PurchaseOption": "All Upfront"
          },
          "priceDimensions": {
            "SKU_MEDIUM.RI3ALL.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0000000000"}},
            "SKU_MEDIUM.RI3ALL.QTY": {"unit": "Quantity", "pricePerUnit": {"USD": "411"}}
          }
        }
      }
    }
  }
}`
