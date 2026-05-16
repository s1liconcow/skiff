package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/observability"
	"github.com/s1liconcow/skiff/internal/provider"
)

type LogQueryClient interface {
	QueryServiceLogs(ctx context.Context, req QueryLogsRequest) ([]observability.LogRecord, error)
}

type QueryLogsRequest struct {
	LogGroup   string    `json:"log_group"`
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	ReleaseID  string    `json:"release_id,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	Limit      int       `json:"limit,omitempty"`
}

func LogGroupName(env, service string) string {
	return "/skiff/" + env + "/" + service
}

func MissingLogGroupError(logGroup string) *provider.Error {
	return &provider.Error{
		Code:     provider.CodeNotFound,
		Provider: Name,
		Op:       "logs",
		Resource: logGroup,
		Summary:  fmt.Sprintf("CloudWatch log group %s was not found; deploy the service or verify log forwarding", logGroup),
	}
}

func NoLogStreamsError(logGroup string) *provider.Error {
	return &provider.Error{
		Code:     provider.CodeNotFound,
		Provider: Name,
		Op:       "logs",
		Resource: logGroup,
		Summary:  fmt.Sprintf("CloudWatch log group %s has no streams; verify the runner log collector is installed and the workload has emitted stdout/stderr", logGroup),
	}
}

func (p *Provider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.clients.LogQueries == nil {
		return nil, provider.Unsupported(Name, "logs")
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "logs", Summary: "service and env are required"}
	}
	records, err := p.clients.LogQueries.QueryServiceLogs(ctx, QueryLogsRequest{
		LogGroup:   LogGroupName(req.Env, req.Service),
		Service:    req.Service,
		Env:        req.Env,
		ReleaseID:  req.ReleaseID,
		InstanceID: req.InstanceID,
		Since:      req.Since,
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, ClassifyError("logs", err)
	}
	records = filterLogRecords(records, req)
	observability.SortLogs(records)
	if req.Limit > 0 && len(records) > req.Limit {
		records = records[len(records)-req.Limit:]
	}
	out := make([]provider.LogEntry, 0, len(records))
	for _, record := range records {
		fields := cloneStringMap(record.Fields)
		if fields == nil {
			fields = map[string]string{}
		}
		fields["service"] = firstNonEmpty(record.Identity.Service, req.Service)
		fields["env"] = firstNonEmpty(record.Identity.Env, req.Env)
		if release := firstNonEmpty(record.Identity.Release, req.ReleaseID); release != "" {
			fields["release"] = release
		}
		if instance := firstNonEmpty(record.Identity.Instance, req.InstanceID); instance != "" {
			fields["instance"] = instance
		}
		if region := firstNonEmpty(record.Identity.Region, p.cfg.Region); region != "" {
			fields["region"] = region
		}
		if record.Identity.Zone != "" {
			fields["zone"] = record.Identity.Zone
		}
		out = append(out, provider.LogEntry{
			Timestamp: record.Timestamp.UTC(),
			Message:   record.Message,
			Source:    record.Source,
			Fields:    fields,
		})
	}
	return &provider.LogsResult{Entries: out}, nil
}

func filterLogRecords(records []observability.LogRecord, req provider.LogsRequest) []observability.LogRecord {
	out := records[:0]
	for _, record := range records {
		if !req.Since.IsZero() && record.Timestamp.Before(req.Since) {
			continue
		}
		if req.ReleaseID != "" && logRecordValue(record, record.Identity.Release, "release", "release_id") != req.ReleaseID {
			continue
		}
		if req.InstanceID != "" && logRecordValue(record, record.Identity.Instance, "instance", "instance_id") != req.InstanceID {
			continue
		}
		out = append(out, record)
	}
	return out
}

func logRecordValue(record observability.LogRecord, primary string, fields ...string) string {
	if primary != "" {
		return primary
	}
	for _, field := range fields {
		if value := record.Fields[field]; value != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
