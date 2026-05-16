package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedenceFileEnvFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skiff.yaml")
	if err := os.WriteFile(path, []byte(`
env: prod
provider: aws
region: us-west-2
stateBucket: s3://from-file
mode: direct
logLevel: warn
`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{
		ConfigPath: path,
		Env: map[string]string{
			"SKIFF_REGION":       "us-east-1",
			"SKIFF_STATE_BUCKET": "s3://from-env",
		},
		Overrides: map[string]string{
			FieldStateBucket: "memory://from-flag",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.Config.Region != "us-east-1" {
		t.Fatalf("region = %q, want env override", loaded.Config.Region)
	}
	if loaded.Config.StateBucket != "memory://from-flag" {
		t.Fatalf("state bucket = %q, want flag override", loaded.Config.StateBucket)
	}
	if loaded.Sources[FieldRegion] != "env" {
		t.Fatalf("region source = %q, want env", loaded.Sources[FieldRegion])
	}
	if loaded.Sources[FieldStateBucket] != "flag" {
		t.Fatalf("state bucket source = %q, want flag", loaded.Sources[FieldStateBucket])
	}
}

func TestValidateModes(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "direct",
			cfg: Config{
				Mode:        ModeDirect,
				Env:         "prod",
				Provider:    "aws",
				Region:      "us-west-2",
				StateBucket: "s3://skiff-state-prod",
			},
		},
		{
			name: "api",
			cfg: Config{
				Mode:   ModeAPI,
				APIURL: "https://skiff.example.com",
			},
		},
		{
			name: "skiffd",
			cfg: Config{
				Mode:        ModeSkiffd,
				Env:         "prod",
				Provider:    "aws",
				Region:      "us-west-2",
				StateBucket: "s3://skiff-state-prod",
			},
		},
		{
			name: "runner",
			cfg: Config{
				Mode:        ModeRunner,
				Env:         "prod",
				Provider:    "aws",
				Region:      "us-west-2",
				StateBucket: "s3://skiff-state-prod",
				Service:     "payments-api",
				ControlKey:  "services/payments-api/control.json",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loaded := Loaded{Config: tc.cfg, Sources: map[string]string{}}
			if err := Validate(loaded); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateReportsRequiredFieldSource(t *testing.T) {
	loaded := Loaded{
		Config: Config{Mode: ModeRunner, Env: "prod"},
		Sources: map[string]string{
			FieldMode: "flag",
			FieldEnv:  "file:runner.json",
		},
	}

	err := Validate(loaded)
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Validate() error = %T, want ValidationError", err)
	}
	if len(validation.Fields) == 0 {
		t.Fatal("expected validation fields")
	}
	found := false
	for _, field := range validation.Fields {
		if field.Field == FieldStateBucket && field.Source == "unset" && field.Code == "REQUIRED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing state bucket required error: %+v", validation.Fields)
	}
}

func TestParseRunnerUserDataStrict(t *testing.T) {
	cfg, err := ParseRunnerUserData([]byte(`{
		"skiff": {
			"env": "prod",
			"service": "payments-api",
			"provider": "aws",
			"region": "us-west-2",
			"state_bucket": "s3://skiff-state-prod",
			"control_key": "services/payments-api/control.json",
			"release_id": "rel_01JABC",
			"logs": {
				"provider": "aws-cloudwatch",
				"group": "/skiff/prod/payments-api",
				"stream_template": "{service}/{release}/{instance}",
				"archive_prefix": "services/payments-api/log-archives/prod/",
				"labels": {
					"service": "payments-api",
					"env": "prod",
					"release": "rel_01JABC"
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeRunner || cfg.Service != "payments-api" || cfg.ControlKey == "" || cfg.ReleaseID != "rel_01JABC" {
		t.Fatalf("unexpected runner config: %+v", cfg)
	}
	if cfg.Logs == nil || cfg.Logs.Group != "/skiff/prod/payments-api" || cfg.Logs.Labels["release"] != "rel_01JABC" {
		t.Fatalf("unexpected runner logs config: %+v", cfg.Logs)
	}

	if _, err := ParseRunnerUserData([]byte(`{"skiff":{"env":"prod","unknown":"x"}}`)); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadRunnerUserDataPreservesLogConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user-data.json")
	if err := os.WriteFile(path, []byte(`{
		"skiff": {
			"env": "prod",
			"service": "payments-api",
			"provider": "aws",
			"region": "us-west-2",
			"state_bucket": "s3://skiff-state-prod",
			"control_key": "services/payments-api/control.json",
			"release_id": "rel_01JABC",
			"logs": {
				"provider": "aws-cloudwatch",
				"group": "/skiff/prod/payments-api",
				"stream_template": "{service}/{release}/{instance}",
				"archive_prefix": "services/payments-api/log-archives/prod/",
				"labels": {"service": "payments-api", "env": "prod"}
			}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(LoadOptions{UserDataPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.ReleaseID != "rel_01JABC" || loaded.Config.Logs == nil || loaded.Config.Logs.Group != "/skiff/prod/payments-api" {
		t.Fatalf("loaded runner user-data lost log config: %+v", loaded.Config)
	}
	if loaded.Sources[FieldLogs] != "user-data:"+path {
		t.Fatalf("logs source = %q", loaded.Sources[FieldLogs])
	}
}

func TestLoadRejectsUnknownFileField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skiff.yaml")
	if err := os.WriteFile(path, []byte("plaintextSecret: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{ConfigPath: path})
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}
