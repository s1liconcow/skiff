package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/config"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
