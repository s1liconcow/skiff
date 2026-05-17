package status_test

import (
	"context"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/status"
)

func TestBuildMultiRegionStatus(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: MultiRegionStack
metadata:
  name: orders
  env: prod
multiRegion:
  primaryRegion: us-west-2
  secondaryRegions:
    - us-east-1
  service:
    name: api
    artifact:
      type: oci
      ref: registry.example.com/orders-api@sha256:abc123
    runtime:
      port: 8080
      health:
        path: /healthz
  database:
    name: db
    engine: postgres
    version: "16"
    size: small
  trafficPolicy:
    host: orders.example.com
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := status.BuildMultiRegionStatus(status.MultiRegionStatusRequest{
		Graph: graph,
		Observations: []status.RegionObservation{{
			Region:         "us-east-1",
			ServiceHealth:  "healthy",
			ReplicationLag: "4s",
			FreshAt:        "2026-05-17T04:00:00Z",
		}},
	})
	if err != nil {
		t.Fatalf("BuildMultiRegionStatus: %v", err)
	}
	if result.PrimaryRegion != "us-west-2" || result.TrafficHost != "orders.example.com" || len(result.Regions) != 2 {
		t.Fatalf("status = %+v", result)
	}
	if result.Regions[0].Region != "us-west-2" || result.Regions[0].DatabaseRole != "primary" || result.Regions[0].TrafficWeight != 100 {
		t.Fatalf("primary region status = %+v", result.Regions[0])
	}
	if result.Regions[1].Region != "us-east-1" || result.Regions[1].DatabaseRole != "replica" || result.Regions[1].ReplicationLag != "4s" || result.Regions[1].ServiceHealth != "healthy" {
		t.Fatalf("secondary region status = %+v", result.Regions[1])
	}
}
