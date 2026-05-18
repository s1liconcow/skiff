package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type runnerUserData struct {
	Skiff runnerConfig `json:"skiff"`
}

type runnerConfig struct {
	Env                string          `json:"env"`
	Service            string          `json:"service"`
	Provider           string          `json:"provider"`
	Region             string          `json:"region"`
	StateBucket        string          `json:"state_bucket"`
	ControlKey         string          `json:"control_key"`
	ReleaseID          string          `json:"release_id,omitempty"`
	ReleaseManifestKey string          `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string          `json:"runtime_manifest_key,omitempty"`
	KMSKey             string          `json:"kms_key,omitempty"`
	LogLevel           string          `json:"log_level,omitempty"`
	Logs               *Logs           `json:"logs,omitempty"`
	Stateful           *runnerStateful `json:"stateful,omitempty"`
}

type runnerStateful struct {
	Group           string `json:"group"`
	Member          int    `json:"member"`
	Generation      int64  `json:"generation,omitempty"`
	VolumeMountPath string `json:"volume_mount_path,omitempty"`
	StableHostname  string `json:"stable_hostname,omitempty"`
	Recipe          string `json:"recipe,omitempty"`
}

func ParseRunnerUserData(body []byte) (Config, error) {
	var raw runnerUserData
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("runner user-data: parse JSON: %w", err)
	}
	cfg := Config{
		Mode:               ModeRunner,
		Env:                raw.Skiff.Env,
		Service:            raw.Skiff.Service,
		Provider:           raw.Skiff.Provider,
		Region:             raw.Skiff.Region,
		StateBucket:        raw.Skiff.StateBucket,
		ControlKey:         raw.Skiff.ControlKey,
		ReleaseID:          raw.Skiff.ReleaseID,
		ReleaseManifestKey: raw.Skiff.ReleaseManifestKey,
		RuntimeManifestKey: raw.Skiff.RuntimeManifestKey,
		KMSKey:             raw.Skiff.KMSKey,
		LogLevel:           raw.Skiff.LogLevel,
	}
	if raw.Skiff.Stateful != nil {
		cfg.StatefulGroup = raw.Skiff.Stateful.Group
		cfg.StatefulMember = raw.Skiff.Stateful.Member
		cfg.StatefulGeneration = raw.Skiff.Stateful.Generation
		cfg.StatefulVolumeMountPath = raw.Skiff.Stateful.VolumeMountPath
		cfg.StatefulStableHostname = raw.Skiff.Stateful.StableHostname
		cfg.StatefulRecipe = raw.Skiff.Stateful.Recipe
	}
	if raw.Skiff.Logs != nil {
		logs := *raw.Skiff.Logs
		logs.Labels = cloneStringMap(logs.Labels)
		cfg.Logs = &logs
	}
	return cfg, nil
}
