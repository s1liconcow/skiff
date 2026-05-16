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
			body = `{"ok":true,"status":{"mode":"api","env":"prod","provider":"aws","region":"us-west-2","state_bucket":"s3://skiff-state-prod","source":"api","freshness":{"source":"memory_index","ready":true,"generation":7},"services":[{"service":"payments-api","env":"prod","desired_release":"rel_03","health":"nominal"}]}}`
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
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
