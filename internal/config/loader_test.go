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

func TestLoadSkiffConfigContextFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".skiffconfig")
	if err := os.WriteFile(path, []byte(`
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local
contexts:
  - name: local
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: file:///tmp/skiff-state
  - name: prod
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-state-prod
`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{
		ConfigPath: path,
		Env: map[string]string{
			"SKIFF_CONTEXT": "prod",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Context != "prod" || loaded.ConfigPath != path {
		t.Fatalf("context/path = %q/%q, want prod/%s", loaded.Context, loaded.ConfigPath, path)
	}
	if loaded.Config.Provider != "aws" || loaded.Config.StateBucket != "s3://skiff-state-prod" {
		t.Fatalf("unexpected selected config: %+v", loaded.Config)
	}
	if loaded.Sources[FieldProvider] != "file:"+path+"#context:prod" {
		t.Fatalf("provider source = %q", loaded.Sources[FieldProvider])
	}
}

func TestLoadSkiffConfigEnvironmentClassAndReleasePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".skiffconfig")
	if err := os.WriteFile(path, []byte(`
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
currentContext: dev
contexts:
  - name: dev
    context:
      mode: direct
      env: david-dev
      environmentClass: development
      provider: aws
      region: us-west-2
      state: s3://skiff-state-dev
      releasePolicy:
        requireSignedReleases: false
        allowUnsignedCode: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}
	policy, err := EffectiveReleasePolicy(loaded.Config)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.EnvironmentClass != "development" || policy.RequireSignedReleases || !policy.AllowUnsignedCode {
		t.Fatalf("unexpected security posture: config=%+v policy=%+v", loaded.Config, policy)
	}
	if loaded.Sources[FieldEnvironmentClass] == "" || loaded.Sources[FieldAllowUnsignedCode] == "" {
		t.Fatalf("missing field sources: %+v", loaded.Sources)
	}
}

func TestLoadSkiffConfigContextFromConfigPathFragment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".skiffconfig")
	if err := os.WriteFile(path, []byte(`
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local
contexts:
  - name: local
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: file:///tmp/skiff-state
  - name: prod
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-state-prod
`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{
		ConfigPath: path + "#prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigPath != path || loaded.Context != "prod" {
		t.Fatalf("context/path = %q/%q, want prod/%s", loaded.Context, loaded.ConfigPath, path)
	}
	if loaded.Config.Provider != "aws" || loaded.Config.StateBucket != "s3://skiff-state-prod" {
		t.Fatalf("unexpected selected config: %+v", loaded.Config)
	}

	loaded, err = Load(LoadOptions{
		Env: map[string]string{
			"SKIFF_CONFIG":  path + "#prod",
			"SKIFF_CONTEXT": "local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigPath != path || loaded.Context != "local" {
		t.Fatalf("context/path = %q/%q, want local/%s", loaded.Context, loaded.ConfigPath, path)
	}
	if loaded.Config.Provider != "fake" || loaded.Config.StateBucket != "file:///tmp/skiff-state" {
		t.Fatalf("explicit context should override config fragment: %+v", loaded.Config)
	}
}

func TestSkiffConfigUseContextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".skiffconfig")
	file := &SkiffConfigFile{
		CurrentContext: "local",
		Contexts: []NamedContext{
			{
				Name: "local",
				Context: ContextConfig{
					Mode:     ModeDirect,
					Env:      "prod",
					Provider: "fake",
					Region:   "local",
					State:    "file:///tmp/skiff-state",
				},
			},
			{
				Name: "prod",
				Context: ContextConfig{
					Mode:     ModeDirect,
					Env:      "prod",
					Provider: "aws",
					Region:   "us-west-2",
					State:    "s3://skiff-state-prod",
				},
			},
		},
	}
	if err := WriteSkiffConfigFile(path, file); err != nil {
		t.Fatal(err)
	}
	loadedFile, err := LoadSkiffConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadedFile.SetCurrentContext("prod"); err != nil {
		t.Fatal(err)
	}
	if err := WriteSkiffConfigFile(path, loadedFile); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadSkiffConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Current() != "prod" {
		t.Fatalf("current context = %q, want prod", reloaded.Current())
	}
	_, values, err := reloaded.SelectContext("")
	if err != nil {
		t.Fatal(err)
	}
	if values[FieldStateBucket] != "s3://skiff-state-prod" {
		t.Fatalf("state = %q", values[FieldStateBucket])
	}
}

func TestLoadAWSLiveApplyInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skiff.yaml")
	if err := os.WriteFile(path, []byte(`
env: prod
provider: aws
region: us-west-2
stateBucket: s3://from-file
mode: direct
awsLiveApply: true
awsVpcId: vpc-file
awsSubnetIds: subnet-a, subnet-b
awsAmiId: ami-file
awsAlbListenerArn: arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff/abc/def
awsLoadBalancerSecurityGroupRef: sg-file
`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{
		ConfigPath: path,
		Env: map[string]string{
			"SKIFF_AWS_VPC_ID":     "vpc-env",
			"SKIFF_AWS_SUBNET_IDS": "subnet-env-a,subnet-env-b",
		},
		Overrides: map[string]string{
			FieldAWSAMIID: "ami-flag",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(loaded); err != nil {
		t.Fatal(err)
	}

	if !loaded.Config.AWSLiveApply {
		t.Fatalf("aws live apply should be enabled: %+v", loaded.Config)
	}
	if loaded.Config.AWSVPCID != "vpc-env" {
		t.Fatalf("aws vpc id = %q, want env override", loaded.Config.AWSVPCID)
	}
	if got := loaded.Config.AWSSubnetIDs; len(got) != 2 || got[0] != "subnet-env-a" || got[1] != "subnet-env-b" {
		t.Fatalf("aws subnet ids = %+v, want env split list", got)
	}
	if loaded.Config.AWSAMIID != "ami-flag" {
		t.Fatalf("aws ami id = %q, want flag override", loaded.Config.AWSAMIID)
	}
	if loaded.Config.AWSALBListenerARN == "" || loaded.Config.AWSLoadBalancerSecurityGroupRef != "sg-file" {
		t.Fatalf("aws live fields not loaded: %+v", loaded.Config)
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
