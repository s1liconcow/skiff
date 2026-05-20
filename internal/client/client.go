package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	skiffevents "github.com/s1liconcow/skiff/internal/events"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	s3store "github.com/s1liconcow/skiff/internal/objstore/s3"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

const (
	ExitUserError     = 1
	ExitProviderError = 3
	ExitTimeout       = 7
	ExitInternalError = 8
)

type Interface interface {
	Version(ctx context.Context, opts VersionOptions) (*Version, error)
	Status(ctx context.Context, opts StatusOptions) (*Status, error)
	Doctor(ctx context.Context, opts DoctorOptions) (*Doctor, error)
	Events(ctx context.Context, opts EventOptions) (*EventList, error)
}

type VersionOptions struct {
	Binary  string
	TraceID string
}

type Version struct {
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

type StatusOptions struct {
	Service string
	Fresh   bool
	TraceID string
}

type Status = servicestatus.Result
type ServiceStatus = servicestatus.Service
type StatefulGroup = servicestatus.StatefulGroup
type StatefulMember = servicestatus.StatefulMember
type StatefulBackup = servicestatus.StatefulBackup
type Freshness = servicestatus.Freshness
type Finding = servicestatus.Finding
type DependencyStatus = servicestatus.DependencyStatus
type ResourceSummary = servicestatus.ResourceSummary

type DoctorOptions struct {
	Service string
	Fresh   bool
	TraceID string
}

type Doctor = servicedoctor.Result

type EventOptions struct {
	Scope     string `json:"scope,omitempty"`
	Service   string `json:"service,omitempty"`
	Operation string `json:"operation,omitempty"`
	Saga      string `json:"saga,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Fresh     bool   `json:"fresh,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

type EventList struct {
	Scope     EventOptions   `json:"scope"`
	Events    []schema.Event `json:"events"`
	Freshness Freshness      `json:"freshness"`
	Findings  []Finding      `json:"findings,omitempty"`
	Source    string         `json:"source"`
}

type SagaOptions struct {
	Saga    string `json:"saga,omitempty"`
	Fresh   bool   `json:"fresh,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

type SagaSummary = stateindex.SagaSummary

type SagaList struct {
	Sagas     []SagaSummary `json:"sagas"`
	Freshness Freshness     `json:"freshness"`
	Findings  []Finding     `json:"findings,omitempty"`
	Source    string        `json:"source"`
}

type SagaInspectOptions struct {
	Saga    string `json:"saga"`
	TraceID string `json:"trace_id,omitempty"`
}

type OperationInspectOptions struct {
	Service   string `json:"service"`
	Operation string `json:"operation"`
	TraceID   string `json:"trace_id,omitempty"`
}

type EventWatcher interface {
	WatchEvents(ctx context.Context, opts EventWatchOptions) (<-chan EventDelivery, error)
}

type EventWatchOptions struct {
	EventOptions
	AfterID      string
	PollInterval time.Duration
	Buffer       int
	Once         bool
}

type EventDelivery struct {
	Event          schema.Event `json:"event,omitempty"`
	ResyncRequired bool         `json:"resync_required,omitempty"`
	LastEventID    string       `json:"last_event_id,omitempty"`
}

type Error struct {
	Code     string
	Summary  string
	ExitCode int
	Err      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return e.Summary
	}
	return e.Summary + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Fail(code, summary string, exitCode int, err error) error {
	if exitCode == 0 {
		exitCode = ExitUserError
	}
	return &Error{Code: code, Summary: summary, ExitCode: exitCode, Err: err}
}

func ExitCode(err error) int {
	var clientErr *Error
	if errors.As(err, &clientErr) {
		return clientErr.ExitCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ExitTimeout
	}
	return ExitInternalError
}

func ErrorCode(err error) string {
	var clientErr *Error
	if errors.As(err, &clientErr) && clientErr.Code != "" {
		return clientErr.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	return "INTERNAL_ERROR"
}

func ErrorSummary(err error) string {
	var clientErr *Error
	if errors.As(err, &clientErr) && clientErr.Summary != "" {
		return clientErr.Summary
	}
	return err.Error()
}

func New(cfg config.Config, opts Options) (Interface, error) {
	switch cfg.Mode {
	case config.ModeAPI, "":
		return NewAPI(cfg, APIOptions{HTTPClient: opts.HTTPClient})
	case config.ModeDirect:
		return NewDirect(cfg, DirectOptions{
			Store:     opts.Store,
			Clock:     opts.Clock,
			BuildInfo: opts.BuildInfo,
		})
	default:
		return nil, Fail("UNSUPPORTED_CLIENT_MODE", fmt.Sprintf("mode %q does not support skiff client operations", cfg.Mode), ExitUserError, nil)
	}
}

type Options struct {
	Store      objstore.ObjectStore
	HTTPClient *http.Client
	Clock      func() time.Time
	BuildInfo  buildinfo.Info
}

type DirectOptions struct {
	Store     objstore.ObjectStore
	Clock     func() time.Time
	BuildInfo buildinfo.Info
}

type Direct struct {
	cfg   config.Config
	store objstore.ObjectStore
	clock func() time.Time
	info  buildinfo.Info
}

func NewDirect(cfg config.Config, opts DirectOptions) (*Direct, error) {
	store := opts.Store
	var err error
	if store == nil {
		store, err = OpenObjectStore(cfg)
		if err != nil {
			return nil, err
		}
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	info := opts.BuildInfo
	if info.Binary == "" {
		info = buildinfo.Current("skiff")
	}
	return &Direct{cfg: cfg, store: store, clock: clock, info: info}, nil
}

func (c *Direct) Version(ctx context.Context, opts VersionOptions) (*Version, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info := c.info
	if opts.Binary != "" && info.Binary == "" {
		info.Binary = opts.Binary
	}
	return &Version{
		Binary:    info.Binary,
		Version:   info.Version,
		Commit:    info.Commit,
		BuildDate: info.BuildDate,
	}, nil
}

func (c *Direct) Status(ctx context.Context, opts StatusOptions) (*Status, error) {
	snapshot, err := stateindex.BuildSnapshot(ctx, c.store, stateindex.BuildOptions{
		Now:               c.clock().UTC(),
		Generation:        1,
		RecentEventsLimit: stateindex.DefaultRecentEventsLimit,
	})
	if err != nil {
		return nil, err
	}
	freshness := servicestatus.FreshnessFromIndex(stateindex.FreshnessFromSnapshot(snapshot, c.clock().UTC(), "direct_object_store"))
	status := servicestatus.FromSnapshot(snapshot, servicestatus.Options{
		Mode:        config.ModeDirect,
		Env:         c.cfg.Env,
		Provider:    c.cfg.Provider,
		Region:      c.cfg.Region,
		StateBucket: redactStateBucket(c.cfg.StateBucket),
		Freshness:   freshness,
		Source:      "direct",
		Service:     opts.Service,
		Now:         c.clock().UTC(),
	})
	return &status, nil
}

func (c *Direct) Doctor(ctx context.Context, opts DoctorOptions) (*Doctor, error) {
	status, err := c.Status(ctx, StatusOptions{Service: opts.Service, Fresh: opts.Fresh, TraceID: opts.TraceID})
	if err != nil {
		return nil, err
	}
	result, err := servicedoctor.Diagnose(ctx, *status, servicedoctor.Options{
		Service: opts.Service,
		TraceID: opts.TraceID,
		Binary:  "skiff",
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Direct) Events(ctx context.Context, opts EventOptions) (*EventList, error) {
	events, findings, err := scanEvents(ctx, c.store, opts)
	if err != nil {
		return nil, err
	}
	freshness := Freshness{
		Source:      "direct_object_store",
		Ready:       true,
		RefreshedAt: c.clock().UTC(),
		Findings:    findings,
	}
	return &EventList{
		Scope:     opts,
		Events:    events,
		Freshness: freshness,
		Findings:  findings,
		Source:    "direct",
	}, nil
}

func (c *Direct) Sagas(ctx context.Context, opts SagaOptions) (*SagaList, error) {
	snapshot, err := stateindex.BuildSnapshot(ctx, c.store, stateindex.BuildOptions{
		Now:               c.clock().UTC(),
		Generation:        1,
		RecentEventsLimit: stateindex.DefaultRecentEventsLimit,
	})
	if err != nil {
		return nil, err
	}
	sagas := append([]SagaSummary(nil), snapshot.Sagas...)
	if opts.Saga != "" {
		filtered := sagas[:0]
		for _, saga := range sagas {
			if saga.SagaID == opts.Saga {
				filtered = append(filtered, saga)
			}
		}
		sagas = filtered
	}
	freshness := servicestatus.FreshnessFromIndex(stateindex.FreshnessFromSnapshot(snapshot, c.clock().UTC(), "direct_object_store"))
	return &SagaList{
		Sagas:     sagas,
		Freshness: freshness,
		Findings:  freshness.Findings,
		Source:    "direct",
	}, nil
}

func (c *Direct) WatchEvents(ctx context.Context, opts EventWatchOptions) (<-chan EventDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = 16
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	out := make(chan EventDelivery, buffer)
	go func() {
		defer close(out)
		afterID := opts.AfterID
		seen := make(map[string]struct{})
		for {
			events, _, err := scanEvents(ctx, c.store, opts.EventOptions)
			if err == nil {
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
				for _, event := range events {
					if event.ID == "" {
						continue
					}
					if afterID != "" && event.ID <= afterID {
						continue
					}
					if _, ok := seen[event.ID]; ok {
						continue
					}
					seen[event.ID] = struct{}{}
					afterID = event.ID
					select {
					case out <- EventDelivery{Event: event, LastEventID: event.ID}:
						if opts.Once {
							return
						}
					case <-ctx.Done():
						return
					}
				}
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return out, nil
}

func OpenObjectStore(cfg config.Config) (objstore.ObjectStore, error) {
	value := strings.TrimSpace(cfg.StateBucket)
	if value == "" {
		return nil, Fail("STATE_REQUIRED", "direct mode requires --state or state_bucket in config", ExitUserError, nil)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return nil, Fail("STATE_INVALID", "state bucket must be an absolute URI", ExitUserError, err)
	}
	switch parsed.Scheme {
	case "file":
		store, err := file.NewFromURI(value)
		if err != nil {
			return nil, Fail("STATE_INVALID", "file state bucket is invalid", ExitUserError, err)
		}
		return store, nil
	case "memory":
		return memory.New(), nil
	case "s3":
		bucket := parsed.Host
		if bucket == "" {
			return nil, Fail("STATE_INVALID", "s3 state URI must include a bucket", ExitUserError, nil)
		}
		store, err := s3store.NewFromEnv(bucket, cfg.Region, s3store.Options{KMSKeyID: cfg.KMSKey})
		if err != nil {
			return nil, Fail("OBJECT_STORE_OPEN_FAILED", "open s3 state bucket", ExitProviderError, err)
		}
		return store, nil
	default:
		return nil, Fail("STATE_INVALID", fmt.Sprintf("unsupported state bucket scheme %q", parsed.Scheme), ExitUserError, nil)
	}
}

func scanObjectState(ctx context.Context, store objstore.ObjectStore, serviceFilter string, eventLimit int) ([]ServiceStatus, []schema.Event, []Finding, error) {
	serviceMetas, err := store.List(ctx, "services/", objstore.ListOptions{})
	if err != nil {
		return nil, nil, nil, Fail("OBJECT_STORE_LIST_FAILED", "list service controls", ExitProviderError, err)
	}
	var services []ServiceStatus
	var findings []Finding
	for _, meta := range serviceMetas {
		if !strings.HasSuffix(meta.Key, "/control.json") {
			continue
		}
		service, ok := serviceFromControlKey(meta.Key)
		if !ok {
			findings = append(findings, Finding{Code: "MALFORMED_SERVICE_CONTROL_KEY", Summary: "service control key does not match services/<service>/control.json", Key: meta.Key})
			continue
		}
		if serviceFilter != "" && service != serviceFilter {
			continue
		}
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, nil, nil, Fail("OBJECT_STORE_GET_FAILED", "read service control", ExitProviderError, err)
		}
		var control schema.ServiceControl
		if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
			findings = append(findings, Finding{Code: "MALFORMED_SERVICE_CONTROL", Summary: err.Error(), Key: meta.Key})
			continue
		}
		status := ServiceStatus{
			Service:        defaultString(control.Service, service),
			Env:            control.Env,
			DesiredRelease: control.DesiredRelease,
			StableRelease:  control.StableRelease,
			UpdatedAt:      control.UpdatedAt,
		}
		if control.Operation != nil {
			status.OperationID = control.Operation.ID
			status.OperationKind = control.Operation.Kind
			status.OperationState = control.Operation.State
		}
		services = append(services, status)
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Service == services[j].Service {
			return services[i].Env < services[j].Env
		}
		return services[i].Service < services[j].Service
	})

	events, eventFindings, err := scanEvents(ctx, store, EventOptions{Limit: eventLimit})
	if err != nil {
		return nil, nil, nil, err
	}
	findings = append(findings, eventFindings...)
	return services, events, findings, nil
}

func scanEvents(ctx context.Context, store objstore.ObjectStore, opts EventOptions) ([]schema.Event, []Finding, error) {
	prefixes, err := eventPrefixes(opts)
	if err != nil {
		return nil, nil, Fail("EVENT_SCOPE_INVALID", err.Error(), ExitUserError, err)
	}
	var metas []objstore.ObjectMeta
	for _, prefix := range prefixes {
		listed, err := store.List(ctx, prefix, objstore.ListOptions{})
		if err != nil {
			return nil, nil, Fail("OBJECT_STORE_LIST_FAILED", "list events", ExitProviderError, err)
		}
		metas = append(metas, listed...)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Key < metas[j].Key })

	events := make([]schema.Event, 0, len(metas))
	findings := make([]Finding, 0)
	seen := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		if _, ok := seen[meta.Key]; ok {
			continue
		}
		seen[meta.Key] = struct{}{}
		if !isEventKey(meta.Key) {
			continue
		}
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, nil, Fail("OBJECT_STORE_GET_FAILED", "read event", ExitProviderError, err)
		}
		event, err := decodeScannedEvent(obj.Body)
		if err != nil {
			findings = append(findings, Finding{Code: "MALFORMED_EVENT", Summary: err.Error(), Key: meta.Key})
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time == events[j].Time {
			return events[i].ID < events[j].ID
		}
		return events[i].Time < events[j].Time
	})
	if opts.Limit > 0 && opts.Limit < len(events) {
		events = events[len(events)-opts.Limit:]
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, findings, nil
}

func decodeScannedEvent(body []byte) (schema.Event, error) {
	var event schema.Event
	if err := canonical.UnmarshalStrict(body, &event); err == nil {
		return event, nil
	}
	var logged skiffevents.Event
	if err := canonical.UnmarshalStrict(body, &logged); err != nil {
		return schema.Event{}, err
	}
	return schema.Event{
		SchemaVersion: logged.SchemaVersion,
		ID:            logged.ID,
		Time:          logged.Time,
		TraceID:       logged.TraceID,
		Subject:       subjectFromEventScope(logged.Scope),
		Type:          logged.Type,
		Severity:      logged.Severity,
		Actor:         logged.Actor,
		Summary:       logged.Summary,
		Facts:         logged.Facts,
		Data:          logged.Data,
	}, nil
}

func subjectFromEventScope(scope skiffevents.Scope) schema.Target {
	switch scope.Kind {
	case skiffevents.ScopeOperation:
		return schema.Target{Kind: "operation", Name: scope.Operation}
	case skiffevents.ScopeSaga:
		return schema.Target{Kind: "saga", Name: scope.Saga}
	default:
		return schema.Target{Kind: "service", Name: scope.Service}
	}
}

func eventPrefixes(opts EventOptions) ([]string, error) {
	switch defaultString(opts.Scope, "recent") {
	case "recent", "all":
		return []string{"services/", "sagas/"}, nil
	case "service":
		if opts.Service == "" {
			return nil, errors.New("service event scope requires --service")
		}
		prefix, err := paths.ServiceEventsPrefix(opts.Service)
		if err != nil {
			return nil, err
		}
		return []string{prefix, "services/" + opts.Service + "/operations/"}, nil
	case "operation":
		prefix, err := paths.OperationEventsPrefix(opts.Service, opts.Operation)
		if err != nil {
			return nil, err
		}
		return []string{prefix}, nil
	case "saga":
		prefix, err := paths.SagaEventsPrefix(opts.Saga)
		if err != nil {
			return nil, err
		}
		return []string{prefix}, nil
	default:
		return nil, fmt.Errorf("unknown event scope %q", opts.Scope)
	}
}

func serviceFromControlKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "services" || parts[2] != "control.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func isEventKey(key string) bool {
	return strings.Contains(key, "/events/") && strings.HasSuffix(key, ".json")
}

func redactStateBucket(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	return parsed.String()
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

type apiErrorEnvelope struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

func decodeAPIError(resp *http.Response, fallback string) error {
	defer resp.Body.Close()
	var envelope apiErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || envelope.Code == "" {
		return Fail("API_REQUEST_FAILED", fallback, ExitProviderError, fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	return Fail(envelope.Code, envelope.Summary, exitCodeForStatus(resp.StatusCode), nil)
}

func exitCodeForStatus(status int) int {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return 6
	case status >= 500:
		return ExitProviderError
	default:
		return ExitUserError
	}
}
