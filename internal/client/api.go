package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/config"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type APIOptions struct {
	HTTPClient *http.Client
}

type API struct {
	cfg        config.Config
	baseURL    *url.URL
	httpClient *http.Client
}

func NewAPI(cfg config.Config, opts APIOptions) (*API, error) {
	if strings.TrimSpace(cfg.APIURL) == "" {
		return nil, Fail("API_URL_REQUIRED", "api mode requires --api-url or api_url in config", ExitUserError, nil)
	}
	parsed, err := url.Parse(cfg.APIURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, Fail("API_URL_INVALID", "api_url must be an absolute http or https URL", ExitUserError, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, Fail("API_URL_INVALID", "api_url scheme must be http or https", ExitUserError, nil)
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &API{cfg: cfg, baseURL: parsed, httpClient: httpClient}, nil
}

func (c *API) Version(ctx context.Context, opts VersionOptions) (*Version, error) {
	var out struct {
		OK        bool   `json:"ok"`
		Binary    string `json:"binary"`
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildDate string `json:"build_date"`
	}
	if err := c.getJSON(ctx, "/version", opts.TraceID, nil, &out); err != nil {
		return nil, err
	}
	return &Version{Binary: out.Binary, Version: out.Version, Commit: out.Commit, BuildDate: out.BuildDate}, nil
}

func (c *API) Status(ctx context.Context, opts StatusOptions) (*Status, error) {
	query := url.Values{}
	if opts.Fresh {
		query.Set("fresh", "true")
	}
	if opts.Service != "" {
		query.Set("service", opts.Service)
	}
	var body struct {
		OK     bool   `json:"ok"`
		Status Status `json:"status"`
	}
	if err := c.getJSON(ctx, "/v1/status", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	body.Status.Mode = config.ModeAPI
	body.Status.APIURL = c.baseURL.String()
	if body.Status.Env == "" {
		body.Status.Env = c.cfg.Env
	}
	if body.Status.Provider == "" {
		body.Status.Provider = c.cfg.Provider
	}
	if body.Status.Region == "" {
		body.Status.Region = c.cfg.Region
	}
	if body.Status.StateBucket == "" {
		body.Status.StateBucket = redactStateBucket(c.cfg.StateBucket)
	}
	if body.Status.Source == "" {
		body.Status.Source = "api"
	}
	return &body.Status, nil
}

func (c *API) Doctor(ctx context.Context, opts DoctorOptions) (*Doctor, error) {
	query := url.Values{}
	if opts.Fresh {
		query.Set("fresh", "true")
	}
	if opts.Service != "" {
		query.Set("service", opts.Service)
	}
	var body struct {
		OK     bool   `json:"ok"`
		Doctor Doctor `json:"doctor"`
	}
	if err := c.getJSON(ctx, "/v1/doctor", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	if body.Doctor.Source == "" {
		body.Doctor.Source = "api"
	}
	if body.Doctor.Env == "" {
		body.Doctor.Env = c.cfg.Env
	}
	if body.Doctor.Provider == "" {
		body.Doctor.Provider = c.cfg.Provider
	}
	if body.Doctor.Region == "" {
		body.Doctor.Region = c.cfg.Region
	}
	if body.Doctor.TraceID == "" {
		body.Doctor.TraceID = opts.TraceID
	}
	return &body.Doctor, nil
}

func (c *API) Events(ctx context.Context, opts EventOptions) (*EventList, error) {
	query := url.Values{}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Fresh {
		query.Set("fresh", "true")
	}
	var body struct {
		OK        bool           `json:"ok"`
		Freshness Freshness      `json:"freshness"`
		Events    []schema.Event `json:"events"`
	}
	if err := c.getJSON(ctx, "/v1/events/recent", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	return &EventList{
		Scope:     opts,
		Events:    body.Events,
		Freshness: body.Freshness,
		Findings:  body.Freshness.Findings,
		Source:    "api",
	}, nil
}

func (c *API) Sagas(ctx context.Context, opts SagaOptions) (*SagaList, error) {
	query := url.Values{}
	if opts.Fresh {
		query.Set("fresh", "true")
	}
	if opts.Saga != "" {
		query.Set("saga", opts.Saga)
	}
	var body struct {
		OK        bool          `json:"ok"`
		Freshness Freshness     `json:"freshness"`
		Sagas     []SagaSummary `json:"sagas"`
	}
	if err := c.getJSON(ctx, "/v1/sagas", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	return &SagaList{
		Sagas:     body.Sagas,
		Freshness: body.Freshness,
		Findings:  body.Freshness.Findings,
		Source:    "api",
	}, nil
}

func (c *API) InspectSaga(ctx context.Context, opts SagaInspectOptions) (*sagastate.InspectResult, error) {
	query := url.Values{}
	query.Set("saga", opts.Saga)
	var body struct {
		OK     bool                    `json:"ok"`
		Result sagastate.InspectResult `json:"result"`
	}
	if err := c.getJSON(ctx, "/v1/sagas/inspect", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	return &body.Result, nil
}

func (c *API) InspectOperation(ctx context.Context, opts OperationInspectOptions) (*opsstate.InspectResult, error) {
	query := url.Values{}
	query.Set("service", opts.Service)
	query.Set("operation", opts.Operation)
	var body struct {
		OK     bool                   `json:"ok"`
		Result opsstate.InspectResult `json:"result"`
	}
	if err := c.getJSON(ctx, "/v1/ops/inspect", opts.TraceID, query, &body); err != nil {
		return nil, err
	}
	return &body.Result, nil
}

func (c *API) CreateProfileOperation(ctx context.Context, req opsstate.ProfileOperationRequest) (*opsstate.ProfileOperationResult, error) {
	var body struct {
		OK     bool                            `json:"ok"`
		Result opsstate.ProfileOperationResult `json:"result"`
	}
	if err := c.postJSON(ctx, "/v1/ops/profile-run", req.Render.TraceID, req, &body); err != nil {
		return nil, err
	}
	return &body.Result, nil
}

func (c *API) WatchEvents(ctx context.Context, opts EventWatchOptions) (<-chan EventDelivery, error) {
	query := url.Values{}
	if opts.Scope != "" {
		query.Set("scope", opts.Scope)
	}
	if opts.Service != "" {
		query.Set("service", opts.Service)
	}
	if opts.Operation != "" {
		query.Set("operation", opts.Operation)
	}
	if opts.Saga != "" {
		query.Set("saga", opts.Saga)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.AfterID != "" {
		query.Set("after", opts.AfterID)
	}
	u := c.baseURL.ResolveReference(&url.URL{Path: "/v1/events/stream"})
	values := u.Query()
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, Fail("API_REQUEST_INVALID", "build event stream request", ExitUserError, err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if opts.TraceID != "" {
		req.Header.Set("X-Skiff-Trace-Id", opts.TraceID)
	}
	if opts.AfterID != "" {
		req.Header.Set("Last-Event-ID", opts.AfterID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, Fail("API_REQUEST_FAILED", "open skiffd event stream", ExitProviderError, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp, fmt.Sprintf("skiffd API returned HTTP %d", resp.StatusCode))
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = 16
	}
	out := make(chan EventDelivery, buffer)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		readSSE(ctx, resp.Body, out, opts.Once)
	}()
	return out, nil
}

func readSSE(ctx context.Context, body io.Reader, out chan<- EventDelivery, once bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventType, eventID string
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 {
			eventType = ""
			eventID = ""
			return true
		}
		payload := strings.TrimSpace(data.String())
		delivery := EventDelivery{LastEventID: eventID}
		if eventType == "resync_required" {
			delivery.ResyncRequired = true
			var body struct {
				LastEventID string `json:"last_event_id"`
				After       string `json:"after"`
			}
			_ = json.Unmarshal([]byte(payload), &body)
			delivery.LastEventID = defaultString(defaultString(body.LastEventID, body.After), eventID)
		} else {
			var event schema.Event
			if err := json.Unmarshal([]byte(payload), &event); err == nil {
				delivery.Event = event
				delivery.LastEventID = defaultString(event.ID, eventID)
			}
		}
		eventType = ""
		eventID = ""
		data.Reset()
		if delivery.Event.ID == "" && !delivery.ResyncRequired {
			return true
		}
		select {
		case out <- delivery:
			if once {
				return false
			}
			return true
		case <-ctx.Done():
			return false
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			eventType = value
		case "id":
			eventID = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	_ = flush()
}

func (c *API) getJSON(ctx context.Context, path string, traceID string, query url.Values, out any) error {
	u := c.baseURL.ResolveReference(&url.URL{Path: path})
	values := u.Query()
	values.Set("format", "json")
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Fail("API_REQUEST_INVALID", "build API request", ExitUserError, err)
	}
	req.Header.Set("Accept", "application/json")
	if traceID != "" {
		req.Header.Set("X-Skiff-Trace-Id", traceID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Fail("API_REQUEST_FAILED", "call skiffd API", ExitProviderError, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp, fmt.Sprintf("skiffd API returned HTTP %d", resp.StatusCode))
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return Fail("API_RESPONSE_INVALID", "decode skiffd API response", ExitProviderError, err)
	}
	return nil
}

func (c *API) postJSON(ctx context.Context, path string, traceID string, value any, out any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return Fail("API_REQUEST_INVALID", "encode API request", ExitUserError, err)
	}
	u := c.baseURL.ResolveReference(&url.URL{Path: path})
	values := u.Query()
	values.Set("format", "json")
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Fail("API_REQUEST_INVALID", "build API request", ExitUserError, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if traceID != "" {
		req.Header.Set("X-Skiff-Trace-Id", traceID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Fail("API_REQUEST_FAILED", "call skiffd API", ExitProviderError, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp, fmt.Sprintf("skiffd API returned HTTP %d", resp.StatusCode))
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return Fail("API_RESPONSE_INVALID", "decode skiffd API response", ExitProviderError, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
