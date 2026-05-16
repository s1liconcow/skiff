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
	Env         string `json:"env"`
	Service     string `json:"service"`
	Provider    string `json:"provider"`
	Region      string `json:"region"`
	StateBucket string `json:"state_bucket"`
	ControlKey  string `json:"control_key"`
	KMSKey      string `json:"kms_key,omitempty"`
	LogLevel    string `json:"log_level,omitempty"`
}

func ParseRunnerUserData(body []byte) (Config, error) {
	var raw runnerUserData
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("runner user-data: parse JSON: %w", err)
	}
	cfg := Config{
		Mode:        ModeRunner,
		Env:         raw.Skiff.Env,
		Service:     raw.Skiff.Service,
		Provider:    raw.Skiff.Provider,
		Region:      raw.Skiff.Region,
		StateBucket: raw.Skiff.StateBucket,
		ControlKey:  raw.Skiff.ControlKey,
		KMSKey:      raw.Skiff.KMSKey,
		LogLevel:    raw.Skiff.LogLevel,
	}
	return cfg, nil
}
