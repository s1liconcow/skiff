package runner

import (
	"encoding/json"
	"errors"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const CollectorConfigSchemaVersion = "skiff.collector-config/v1"

type CollectorConfig struct {
	SchemaVersion string            `json:"schema_version"`
	Service       string            `json:"service"`
	Env           string            `json:"env"`
	ReleaseID     string            `json:"release_id"`
	InstanceID    string            `json:"instance_id,omitempty"`
	Region        string            `json:"region,omitempty"`
	Zone          string            `json:"zone,omitempty"`
	Metrics       *AppMetricsTarget `json:"metrics,omitempty"`
}

type AppMetricsTarget struct {
	Path   string            `json:"path"`
	Port   int               `json:"port"`
	Labels map[string]string `json:"labels,omitempty"`
}

func BuildCollectorConfig(manifest schema.RuntimeManifest, identity *Identity) (*CollectorConfig, error) {
	if manifest.Service == "" || manifest.Env == "" || manifest.ReleaseID == "" {
		return nil, errors.New("runtime manifest service, env, and release_id are required")
	}
	cfg := &CollectorConfig{
		SchemaVersion: CollectorConfigSchemaVersion,
		Service:       manifest.Service,
		Env:           manifest.Env,
		ReleaseID:     manifest.ReleaseID,
	}
	if identity != nil {
		cfg.InstanceID = identity.InstanceID
		cfg.Region = identity.Region
		cfg.Zone = identity.Zone
	}
	if manifest.Metrics != nil && manifest.Metrics.Enabled {
		cfg.Metrics = &AppMetricsTarget{
			Path: manifest.Metrics.Path,
			Port: manifest.Metrics.Port,
			Labels: map[string]string{
				"service": manifest.Service,
				"env":     manifest.Env,
				"release": manifest.ReleaseID,
			},
		}
		if cfg.InstanceID != "" {
			cfg.Metrics.Labels["instance"] = cfg.InstanceID
		}
		if cfg.Region != "" {
			cfg.Metrics.Labels["region"] = cfg.Region
		}
		if cfg.Zone != "" {
			cfg.Metrics.Labels["zone"] = cfg.Zone
		}
	}
	return cfg, nil
}

func RenderCollectorConfig(manifest schema.RuntimeManifest, identity *Identity) ([]byte, error) {
	cfg, err := BuildCollectorConfig(manifest, identity)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(cfg, "", "  ")
}
