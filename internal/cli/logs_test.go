package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestLogsJSONUsesProviderFilters(t *testing.T) {
	clearSkiffEnv(t)
	now := time.Date(2026, 5, 16, 23, 40, 0, 0, time.UTC)
	fake := &fakeLogsProvider{
		result: &provider.LogsResult{Entries: []provider.LogEntry{{
			Timestamp: now,
			Message:   "started",
			Source:    "i-123/stdout",
			Fields:    map[string]string{"service": "payments-api", "release": "rel_02", "instance": "i-123"},
		}}},
	}
	restoreLogsTestHooks(t, fake)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"logs", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--release", "rel_02",
		"--instance", "i-123",
		"--since", "2026-05-16T23:00:00Z",
		"--format", "json",
		"--trace-id", "tr_logs",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if fake.req.Service != "payments-api" || fake.req.Env != "prod" || fake.req.ReleaseID != "rel_02" || fake.req.InstanceID != "i-123" {
		t.Fatalf("unexpected provider request: %+v", fake.req)
	}
	if fake.req.Since.Format(time.RFC3339) != "2026-05-16T23:00:00Z" {
		t.Fatalf("since = %s", fake.req.Since.Format(time.RFC3339Nano))
	}

	var got logsOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("logs output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_logs" || len(got.Entries) != 1 || got.Entries[0].Fields["release"] != "rel_02" {
		t.Fatalf("unexpected logs output: %+v", got)
	}
}

func TestLogsFollowJSONStopsOnCancellation(t *testing.T) {
	clearSkiffEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Date(2026, 5, 16, 23, 45, 0, 0, time.UTC)
	fake := &fakeLogsProvider{
		result: &provider.LogsResult{Entries: []provider.LogEntry{{
			Timestamp: now,
			Message:   "ready",
			Fields:    map[string]string{"service": "payments-api"},
		}}},
		afterCall: cancel,
	}
	restoreLogsTestHooks(t, fake)
	logsContext = func() context.Context { return ctx }
	logsFollowInterval = time.Millisecond

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"logs", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--follow",
		"--format", "json",
		"--trace-id", "tr_follow",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if fake.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", fake.calls)
	}
	var got logsOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("follow logs output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_follow" || len(got.Entries) != 1 || got.Entries[0].Message != "ready" {
		t.Fatalf("unexpected follow output: %+v", got)
	}
}

func TestLogsMissingLogGroupJSONIsActionable(t *testing.T) {
	clearSkiffEnv(t)
	fake := &fakeLogsProvider{err: aws.MissingLogGroupError("/skiff/prod/payments-api")}
	restoreLogsTestHooks(t, fake)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"logs", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_missing_logs",
	}, &stdout, &stderr)
	if code != ExitProviderError {
		t.Fatalf("exit code = %d, want %d; stderr = %s stdout = %s", code, ExitProviderError, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("logs error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != string(provider.CodeNotFound) || got.TraceID != "tr_missing_logs" || len(got.RecommendedActions) == 0 {
		t.Fatalf("unexpected logs error output: %+v", got)
	}
}

func restoreLogsTestHooks(t *testing.T, fake *fakeLogsProvider) {
	t.Helper()
	oldNewLogsProvider := newLogsProvider
	oldLogsContext := logsContext
	oldFollowInterval := logsFollowInterval
	newLogsProvider = func(cfg config.Config) (logsProvider, error) {
		if cfg.Env != "prod" || cfg.Provider != "aws" || cfg.Region != "us-west-2" {
			return nil, errors.New("unexpected config")
		}
		return fake, nil
	}
	t.Cleanup(func() {
		newLogsProvider = oldNewLogsProvider
		logsContext = oldLogsContext
		logsFollowInterval = oldFollowInterval
	})
}

type fakeLogsProvider struct {
	req       provider.LogsRequest
	result    *provider.LogsResult
	err       error
	calls     int
	afterCall func()
}

func (p *fakeLogsProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	p.calls++
	p.req = req
	if p.afterCall != nil {
		p.afterCall()
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.result == nil {
		return &provider.LogsResult{}, nil
	}
	return p.result, nil
}
