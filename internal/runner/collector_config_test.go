package runner

import (
	"encoding/json"
	"testing"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestBuildCollectorConfigIncludesAppMetricsEndpoint(t *testing.T) {
	cfg, err := BuildCollectorConfig(schema.RuntimeManifest{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_01JABC",
		Metrics:   &schema.MetricsConfig{Enabled: true, Path: "/metrics", Port: 8080},
	}, &Identity{InstanceID: "i-123", Region: "us-west-2", Zone: "us-west-2a"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics == nil || cfg.Metrics.Path != "/metrics" || cfg.Metrics.Port != 8080 {
		t.Fatalf("unexpected metrics target: %+v", cfg.Metrics)
	}
	if cfg.Metrics.Labels["service"] != "payments-api" || cfg.Metrics.Labels["instance"] != "i-123" || cfg.Metrics.Labels["zone"] != "us-west-2a" {
		t.Fatalf("unexpected metric labels: %+v", cfg.Metrics.Labels)
	}
	body, err := RenderCollectorConfig(schema.RuntimeManifest{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_01JABC",
		Metrics:   &schema.MetricsConfig{Enabled: true, Path: "/metrics", Port: 8080},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rendered CollectorConfig
	if err := json.Unmarshal(body, &rendered); err != nil {
		t.Fatalf("rendered collector config is not JSON: %v\n%s", err, string(body))
	}
	if rendered.SchemaVersion != CollectorConfigSchemaVersion || rendered.Metrics == nil {
		t.Fatalf("unexpected rendered config: %+v", rendered)
	}
}
