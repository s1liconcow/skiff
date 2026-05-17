package debug

import (
	"context"
	"errors"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const SchemaVersion = "skiff.debug.bundle/v1"

type Collector struct {
	Store    objstore.ObjectStore
	Provider provider.Provider
	Clock    func() time.Time
}

type Request struct {
	Service    string       `json:"service"`
	Env        string       `json:"env"`
	InstanceID string       `json:"instance_id,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	TraceID    string       `json:"trace_id,omitempty"`
	Actor      schema.Actor `json:"actor"`
}

type Bundle struct {
	SchemaVersion   string                        `json:"schema_version"`
	BundleID        string                        `json:"bundle_id"`
	OK              bool                          `json:"ok"`
	Service         string                        `json:"service"`
	Env             string                        `json:"env"`
	InstanceID      string                        `json:"instance_id,omitempty"`
	TraceID         string                        `json:"trace_id,omitempty"`
	CreatedAt       string                        `json:"created_at"`
	Provider        string                        `json:"provider,omitempty"`
	DebugSession    *provider.DebugSession        `json:"debug_session,omitempty"`
	ServiceControl  *schema.ServiceControl        `json:"service_control,omitempty"`
	ReleaseID       string                        `json:"release_id,omitempty"`
	ReleaseDigest   string                        `json:"release_digest,omitempty"`
	RunnerStatus    string                        `json:"runner_status"`
	SystemdStatus   string                        `json:"systemd_status"`
	DiskUsage       string                        `json:"disk_usage"`
	OOMEvents       []string                      `json:"oom_events,omitempty"`
	TargetHealth    string                        `json:"target_health"`
	CollectorStatus string                        `json:"collector_status"`
	Resources       []provider.ResourceInspection `json:"resources,omitempty"`
	Logs            []provider.LogEntry           `json:"logs,omitempty"`
	Metrics         []provider.MetricSeries       `json:"metrics,omitempty"`
	Findings        []Finding                     `json:"findings,omitempty"`
	Redactions      []string                      `json:"redactions,omitempty"`
	NextCommands    []string                      `json:"next_commands,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

func (c Collector) Collect(ctx context.Context, req Request) (*Bundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Provider == nil {
		return nil, errors.New("debug provider is required")
	}
	now := c.now()
	bundle := &Bundle{
		SchemaVersion:   SchemaVersion,
		BundleID:        "dbg_" + events.NewID(now, req.TraceID+req.Service+req.InstanceID),
		OK:              true,
		Service:         req.Service,
		Env:             req.Env,
		InstanceID:      req.InstanceID,
		TraceID:         req.TraceID,
		CreatedAt:       canonical.Time(now),
		Provider:        c.Provider.Name(),
		RunnerStatus:    "not observed",
		SystemdStatus:   "not collected",
		DiskUsage:       "not collected",
		TargetHealth:    "not observed",
		CollectorStatus: "ok",
		Redactions:      []string{"environment variables omitted", "secret values omitted"},
		NextCommands: []string{
			"skiff status " + req.Service + " --fresh --format json --trace-id " + req.TraceID,
			"skiff logs " + req.Service + " --since 20m --format json --trace-id " + req.TraceID,
			"skiff doctor " + req.Service + " --fresh --format json --trace-id " + req.TraceID,
		},
	}
	c.addState(ctx, bundle)
	c.addProviderDebug(ctx, req, bundle)
	c.addProviderInspection(ctx, req, bundle)
	c.addLogs(ctx, req, bundle)
	c.addMetrics(ctx, req, bundle)
	if len(bundle.Findings) > 0 {
		bundle.CollectorStatus = "completed_with_findings"
	}
	return bundle, nil
}

func (c Collector) addState(ctx context.Context, bundle *Bundle) {
	if c.Store == nil {
		bundle.addFinding("STATE_NOT_CONFIGURED", "object state was not available to debug collector", "")
		return
	}
	doc, err := state.NewClient(c.Store).GetServiceControl(ctx, bundle.Service)
	if err != nil {
		bundle.addFinding("SERVICE_CONTROL_NOT_FOUND", "service control could not be read", err.Error())
		return
	}
	bundle.ServiceControl = &doc.Control
	bundle.ReleaseID = firstNonEmpty(doc.Control.StableRelease, doc.Control.DesiredRelease)
	if bundle.ReleaseID == "" {
		bundle.addFinding("RELEASE_NOT_OBSERVED", "service control has no stable or desired release", "")
		return
	}
	key, err := paths.ReleaseManifest(bundle.Service, bundle.ReleaseID)
	if err != nil {
		bundle.addFinding("RELEASE_KEY_INVALID", "release manifest key could not be built", err.Error())
		return
	}
	object, err := c.Store.Get(ctx, key)
	if err != nil {
		bundle.addFinding("RELEASE_NOT_FOUND", "release manifest could not be read", err.Error())
		return
	}
	var manifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(object.Body, &manifest); err != nil {
		bundle.addFinding("RELEASE_DECODE_FAILED", "release manifest could not be decoded", err.Error())
		return
	}
	bundle.ReleaseDigest = manifest.Artifact.Digest
	bundle.RunnerStatus = "release " + bundle.ReleaseID + " observed in object state"
}

func (c Collector) addProviderDebug(ctx context.Context, req Request, bundle *Bundle) {
	session, err := c.Provider.Debug(ctx, provider.DebugRequest{Service: req.Service, Env: req.Env, InstanceID: req.InstanceID, Mode: provider.DebugModeBundle, Reason: req.Reason})
	if err != nil {
		bundle.addFinding("DEBUG_SESSION_UNAVAILABLE", "provider debug session could not be started", err.Error())
		return
	}
	bundle.DebugSession = session
}

func (c Collector) addProviderInspection(ctx context.Context, req Request, bundle *Bundle) {
	inspection, err := c.Provider.InspectService(ctx, provider.ServiceRef{Service: req.Service, Env: req.Env})
	if err != nil {
		bundle.addFinding("INSPECTION_FAILED", "provider service inspection failed", err.Error())
		return
	}
	bundle.Resources = append([]provider.ResourceInspection(nil), inspection.Resources...)
	for _, resource := range inspection.Resources {
		switch resource.Kind {
		case "target-group", "target_group", "aws.target_group":
			bundle.TargetHealth = firstNonEmpty(resource.Status, resource.ProviderID, "observed")
		}
	}
}

func (c Collector) addLogs(ctx context.Context, req Request, bundle *Bundle) {
	result, err := c.Provider.Logs(ctx, provider.LogsRequest{Service: req.Service, Env: req.Env, InstanceID: req.InstanceID, Since: c.now().Add(-20 * time.Minute), Limit: 20})
	if err != nil {
		bundle.addFinding("LOGS_FAILED", "provider logs could not be collected", err.Error())
		return
	}
	bundle.Logs = append([]provider.LogEntry(nil), result.Entries...)
}

func (c Collector) addMetrics(ctx context.Context, req Request, bundle *Bundle) {
	now := c.now()
	result, err := c.Provider.Metrics(ctx, provider.MetricsRequest{Service: req.Service, Env: req.Env, InstanceID: req.InstanceID, From: now.Add(-30 * time.Minute), To: now, Names: []string{"request_count", "cpu_utilization"}, PeriodSeconds: 60})
	if err != nil {
		bundle.addFinding("METRICS_FAILED", "provider metrics could not be collected", err.Error())
		return
	}
	bundle.Metrics = append([]provider.MetricSeries(nil), result.Series...)
}

func (c Collector) now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

func (b *Bundle) addFinding(code, summary, detail string) {
	b.OK = false
	b.Findings = append(b.Findings, Finding{Code: code, Summary: summary, Detail: detail})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
