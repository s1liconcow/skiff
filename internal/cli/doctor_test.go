package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
)

func TestDoctorJSONUsesClientAndKeepsMutatingActionsLabeled(t *testing.T) {
	clearSkiffEnv(t)
	fake := &fakeDoctorClient{doctor: &client.Doctor{
		TraceID: "tr_doctor",
		Service: "payments-api",
		Env:     "prod",
		Health:  "degraded",
		Findings: []servicedoctor.Finding{{
			ID:         "payments-api_target_health_unhealthy",
			Code:       "TARGET_HEALTH_UNHEALTHY",
			Severity:   servicedoctor.SeverityHigh,
			Service:    "payments-api",
			Summary:    "target unhealthy",
			Confidence: 0.83,
		}},
		RecommendedActions: []servicedoctor.RecommendedAction{
			{ID: "payments-api_inspect_logs", Kind: "command", Service: "payments-api", Command: "skiff logs payments-api --since 20m --format json", Mutating: false},
			{ID: "payments-api_rollback_to_stable", Kind: "command", Service: "payments-api", Command: "skiff rollback payments-api --to rel_01 --yes --format json", Mutating: true, Risk: "medium", Reversibility: "reversible"},
		},
	}}
	oldNewDoctorClient := newDoctorClient
	newDoctorClient = func(cfg config.Config, opts client.Options) (client.Interface, error) {
		if cfg.Env != "prod" || cfg.Provider != "aws" || cfg.Region != "us-west-2" {
			return nil, errors.New("unexpected config")
		}
		return fake, nil
	}
	t.Cleanup(func() {
		newDoctorClient = oldNewDoctorClient
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"doctor", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--fresh",
		"--format", "json",
		"--trace-id", "tr_doctor",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got doctorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_doctor" || got.Doctor.Service != "payments-api" || got.Doctor.Health != "degraded" {
		t.Fatalf("unexpected doctor output: %+v", got)
	}
	if len(got.Doctor.RecommendedActions) != 2 || !got.Doctor.RecommendedActions[1].Mutating {
		t.Fatalf("mutating action not labeled: %+v", got.Doctor.RecommendedActions)
	}
	if fake.opts.Service != "payments-api" || !fake.opts.Fresh || fake.opts.TraceID != "tr_doctor" {
		t.Fatalf("doctor opts not propagated: %+v", fake.opts)
	}
}

type fakeDoctorClient struct {
	doctor *client.Doctor
	opts   client.DoctorOptions
}

func (c *fakeDoctorClient) Version(ctx context.Context, opts client.VersionOptions) (*client.Version, error) {
	return &client.Version{}, nil
}

func (c *fakeDoctorClient) Status(ctx context.Context, opts client.StatusOptions) (*client.Status, error) {
	return &client.Status{}, nil
}

func (c *fakeDoctorClient) Doctor(ctx context.Context, opts client.DoctorOptions) (*client.Doctor, error) {
	c.opts = opts
	return c.doctor, nil
}

func (c *fakeDoctorClient) Events(ctx context.Context, opts client.EventOptions) (*client.EventList, error) {
	return &client.EventList{}, nil
}
