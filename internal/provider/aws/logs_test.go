package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/observability"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestLogsQueriesCloudWatchAndNormalizesEntries(t *testing.T) {
	now := time.Date(2026, 5, 16, 23, 20, 0, 0, time.UTC)
	client := &fakeLogQueryClient{records: []observability.LogRecord{
		{
			Timestamp: now.Add(2 * time.Second),
			Message:   "second",
			Source:    "stream-b",
			Identity:  observability.LogIdentity{Service: "payments-api", Env: "prod", Release: "rel_02", Instance: "i-b", Region: "us-west-2", Zone: "us-west-2a"},
		},
		{
			Timestamp: now.Add(time.Second),
			Message:   "first",
			Source:    "stream-a",
			Identity:  observability.LogIdentity{Service: "payments-api", Env: "prod", Instance: "i-a"},
			Fields:    map[string]string{"level": "info", "release_id": "rel_02"},
		},
		{
			Timestamp: now.Add(-time.Second),
			Message:   "old",
			Identity:  observability.LogIdentity{Service: "payments-api", Env: "prod", Release: "rel_01", Instance: "i-old"},
		},
	}}
	p := newLogsProvider(t, client)
	result, err := p.Logs(context.Background(), provider.LogsRequest{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_02",
		Since:     now,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if client.req.LogGroup != "/skiff/prod/payments-api" || client.req.ReleaseID != "rel_02" {
		t.Fatalf("unexpected query request: %+v", client.req)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(result.Entries), result.Entries)
	}
	if result.Entries[0].Message != "first" || result.Entries[1].Message != "second" {
		t.Fatalf("entries not sorted by timestamp: %+v", result.Entries)
	}
	if result.Entries[0].Fields["service"] != "payments-api" || result.Entries[0].Fields["release"] != "rel_02" || result.Entries[0].Fields["level"] != "info" {
		t.Fatalf("entry fields not enriched: %+v", result.Entries[0].Fields)
	}
}

func TestLogsFiltersByInstance(t *testing.T) {
	now := time.Date(2026, 5, 16, 23, 25, 0, 0, time.UTC)
	client := &fakeLogQueryClient{records: []observability.LogRecord{
		{Timestamp: now, Message: "match", Identity: observability.LogIdentity{Release: "rel_02", Instance: "i-keep"}},
		{Timestamp: now, Message: "drop", Identity: observability.LogIdentity{Release: "rel_02", Instance: "i-drop"}},
	}}
	p := newLogsProvider(t, client)
	result, err := p.Logs(context.Background(), provider.LogsRequest{
		Service:    "payments-api",
		Env:        "prod",
		ReleaseID:  "rel_02",
		InstanceID: "i-keep",
	})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Message != "match" || result.Entries[0].Fields["instance"] != "i-keep" {
		t.Fatalf("unexpected filtered entries: %+v", result.Entries)
	}
}

func TestLogsClassifiesPermissionErrors(t *testing.T) {
	p := newLogsProvider(t, &fakeLogQueryClient{err: errors.New("AccessDeniedException: not authorized")})
	_, err := p.Logs(context.Background(), provider.LogsRequest{Service: "payments-api", Env: "prod"})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("logs err = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeAccessDenied {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeAccessDenied)
	}
}

func TestLogsMissingLogGroupAndNoStreamsAreActionable(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		summary string
	}{
		{name: "missing log group", err: aws.MissingLogGroupError("/skiff/prod/payments-api"), summary: "deploy the service"},
		{name: "no streams", err: aws.NoLogStreamsError("/skiff/prod/payments-api"), summary: "runner log collector"},
		{name: "cloudwatch not found string", err: errors.New("ResourceNotFoundException: log group does not exist"), summary: "does not exist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newLogsProvider(t, &fakeLogQueryClient{err: tc.err})
			_, err := p.Logs(context.Background(), provider.LogsRequest{Service: "payments-api", Env: "prod"})
			var providerErr *provider.Error
			if !errors.As(err, &providerErr) {
				t.Fatalf("logs err = %T, want provider.Error", err)
			}
			if providerErr.Code != provider.CodeNotFound {
				t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeNotFound)
			}
			if !strings.Contains(providerErr.Summary, tc.summary) {
				t.Fatalf("summary = %q, want to contain %q", providerErr.Summary, tc.summary)
			}
		})
	}
}

func newLogsProvider(t *testing.T, client *fakeLogQueryClient) *aws.Provider {
	t.Helper()
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{LogQueries: client}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeLogQueryClient struct {
	req     aws.QueryLogsRequest
	records []observability.LogRecord
	err     error
}

func (c *fakeLogQueryClient) QueryServiceLogs(ctx context.Context, req aws.QueryLogsRequest) ([]observability.LogRecord, error) {
	c.req = req
	return c.records, c.err
}
