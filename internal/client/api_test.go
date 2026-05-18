package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
)

func TestAPIStatusUsesSkiffdJSONEndpoints(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("format") != "json" {
			t.Fatalf("missing format=json on %s", req.URL.String())
		}
		if req.Header.Get("X-Skiff-Trace-Id") != "tr_api_status" {
			t.Fatalf("trace header = %q", req.Header.Get("X-Skiff-Trace-Id"))
		}
		var body string
		switch req.URL.Path {
		case "/v1/status":
			body = `{"ok":true,"status":{"mode":"api","env":"prod","provider":"aws","region":"us-west-2","state_bucket":"s3://skiff-state-prod","source":"api","freshness":{"source":"memory_index","ready":true,"generation":7},"services":[{"service":"payments-api","env":"prod","desired_release":"rel_03","health":"nominal"}],"stateful_groups":[{"group":"orders-stream","env":"prod","replicas":1,"health":"nominal","members":[{"member":0,"generation":2,"role":"primary","instance_id":"i-0","volume_id":"vol-0","dns_name":"orders-stream-0.internal","phase":"ready","health":"nominal"}]}]}}`
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	api, err := NewAPI(config.Config{
		Mode:   config.ModeAPI,
		APIURL: "http://skiffd.example.test",
	}, APIOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatalf("new API client: %v", err)
	}
	status, err := api.Status(context.Background(), StatusOptions{TraceID: "tr_api_status"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Mode != config.ModeAPI || status.APIURL != "http://skiffd.example.test" || status.Freshness.Generation != 7 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if len(status.Services) != 1 || status.Services[0].Service != "payments-api" || status.Services[0].DesiredRelease != "rel_03" {
		t.Fatalf("unexpected services: %+v", status.Services)
	}
	if len(status.StatefulGroups) != 1 || status.StatefulGroups[0].Group != "orders-stream" || status.StatefulGroups[0].Members[0].Role != "primary" {
		t.Fatalf("unexpected stateful groups: %+v", status.StatefulGroups)
	}
}

func TestAPIDoctorUsesSkiffdJSONEndpoint(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/doctor" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		if req.URL.Query().Get("format") != "json" || req.URL.Query().Get("fresh") != "true" || req.URL.Query().Get("service") != "payments-api" {
			t.Fatalf("unexpected query %s", req.URL.RawQuery)
		}
		if req.Header.Get("X-Skiff-Trace-Id") != "tr_api_doctor" {
			t.Fatalf("trace header = %q", req.Header.Get("X-Skiff-Trace-Id"))
		}
		body := `{"ok":true,"doctor":{"service":"payments-api","env":"prod","provider":"aws","region":"us-west-2","source":"api","health":"degraded","freshness":{"source":"memory","ready":true},"facts":[{"type":"target_health","message":"1 target unhealthy","service":"payments-api"}],"findings":[{"id":"payments-api_target_health_unhealthy","code":"TARGET_HEALTH_UNHEALTHY","severity":"high","service":"payments-api","summary":"target unhealthy","confidence":0.83}],"hypotheses":[{"id":"payments-api_hypothesis_target_health_bad","service":"payments-api","message":"health check mismatch","confidence":0.72}],"recommended_actions":[{"id":"payments-api_inspect_logs","kind":"command","service":"payments-api","command":"skiff logs payments-api --since 20m --format json","mutating":false}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	api, err := NewAPI(config.Config{
		Mode:   config.ModeAPI,
		APIURL: "http://skiffd.example.test",
	}, APIOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatalf("new API client: %v", err)
	}
	result, err := api.Doctor(context.Background(), DoctorOptions{Service: "payments-api", Fresh: true, TraceID: "tr_api_doctor"})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if result.TraceID != "tr_api_doctor" || result.Service != "payments-api" || result.Health != "degraded" {
		t.Fatalf("unexpected doctor result: %+v", result)
	}
	if len(result.Findings) != 1 || result.Findings[0].Code != "TARGET_HEALTH_UNHEALTHY" {
		t.Fatalf("unexpected findings: %+v", result.Findings)
	}
}

func TestAPIWatchEventsParsesSSEStream(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/events/stream" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		if req.URL.Query().Get("scope") != "saga" || req.URL.Query().Get("saga") != "saga_01JABC" || req.URL.Query().Get("after") != "01JOLD" {
			t.Fatalf("unexpected query %s", req.URL.RawQuery)
		}
		if req.Header.Get("Accept") != "text/event-stream" || req.Header.Get("Last-Event-ID") != "01JOLD" {
			t.Fatalf("unexpected stream headers: %+v", req.Header)
		}
		body := "id: 01JNEW\n" +
			"event: skiff.event\n" +
			`data: {"schema_version":"skiff.state/v1","id":"01JNEW","time":"2026-05-16T19:00:00Z","subject":{"kind":"saga","name":"saga_01JABC"},"type":"approval.required","summary":"approval required"}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	api, err := NewAPI(config.Config{Mode: config.ModeAPI, APIURL: "http://skiffd.example.test"}, APIOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatalf("new API client: %v", err)
	}
	ch, err := api.WatchEvents(context.Background(), EventWatchOptions{
		EventOptions: EventOptions{Scope: "saga", Saga: "saga_01JABC", TraceID: "tr_watch"},
		AfterID:      "01JOLD",
	})
	if err != nil {
		t.Fatalf("watch events: %v", err)
	}
	got, ok := <-ch
	if !ok || got.Event.ID != "01JNEW" || got.Event.Subject.Name != "saga_01JABC" || got.LastEventID != "01JNEW" {
		t.Fatalf("unexpected stream delivery: %+v ok=%v", got, ok)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
