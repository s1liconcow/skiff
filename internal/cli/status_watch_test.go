package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
)

func TestStatusWatchJSONStopsOnCancellation(t *testing.T) {
	clearSkiffEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeStatusClient{status: &client.Status{
		Mode:   config.ModeDirect,
		Source: "direct",
		Services: []client.ServiceStatus{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel_02",
			Health:         "nominal",
		}},
		Freshness: client.Freshness{Source: "direct_object_store", Ready: true},
	}, afterStatus: cancel}
	oldNewStatusClient := newStatusClient
	oldStatusContext := statusContext
	oldStatusInterval := statusWatchInterval
	newStatusClient = func(cfg config.Config, opts client.Options) (client.Interface, error) {
		if cfg.Env != "prod" || cfg.Provider != "aws" || cfg.Region != "us-west-2" {
			return nil, errors.New("unexpected config")
		}
		return fake, nil
	}
	statusContext = func() context.Context { return ctx }
	statusWatchInterval = time.Millisecond
	t.Cleanup(func() {
		newStatusClient = oldNewStatusClient
		statusContext = oldStatusContext
		statusWatchInterval = oldStatusInterval
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"status", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--watch",
		"--format", "json",
		"--trace-id", "tr_status_watch",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("watch JSON output contains ANSI escapes: %q", stdout.String())
	}
	var got statusOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("watch output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_status_watch" || len(got.Status.Services) != 1 || got.Status.Services[0].Health != "nominal" {
		t.Fatalf("unexpected watch status output: %+v", got)
	}
}

type fakeStatusClient struct {
	status      *client.Status
	afterStatus func()
}

func (c *fakeStatusClient) Version(ctx context.Context, opts client.VersionOptions) (*client.Version, error) {
	return &client.Version{}, nil
}

func (c *fakeStatusClient) Status(ctx context.Context, opts client.StatusOptions) (*client.Status, error) {
	if c.afterStatus != nil {
		c.afterStatus()
	}
	return c.status, nil
}

func (c *fakeStatusClient) Doctor(ctx context.Context, opts client.DoctorOptions) (*client.Doctor, error) {
	return &client.Doctor{}, nil
}

func (c *fakeStatusClient) Events(ctx context.Context, opts client.EventOptions) (*client.EventList, error) {
	return &client.EventList{}, nil
}
