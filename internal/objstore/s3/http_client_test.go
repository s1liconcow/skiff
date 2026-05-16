package s3store

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	skiffaws "github.com/s1liconcow/skiff/internal/aws"
)

func TestHTTPClientPutObjectSendsConditionalKMSMetadataAndSignature(t *testing.T) {
	var sawBody string
	client := mustHTTPClientWithTransport(t, "https://s3.test", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/state-bucket/services/api/control.json" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("If-None-Match") != "*" {
			t.Fatalf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		if r.Header.Get("x-amz-server-side-encryption") != serverSideEncryptionAWSKMS {
			t.Fatalf("encryption header = %q", r.Header.Get("x-amz-server-side-encryption"))
		}
		if r.Header.Get("x-amz-server-side-encryption-aws-kms-key-id") != "alias/skiff" {
			t.Fatalf("kms header = %q", r.Header.Get("x-amz-server-side-encryption-aws-kms-key-id"))
		}
		if r.Header.Get("x-amz-meta-service") != "api" {
			t.Fatalf("metadata header = %q", r.Header.Get("x-amz-meta-service"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll body: %v", err)
		}
		sawBody = string(body)
		return response(http.StatusOK, http.Header{
			"ETag":             {`"etag-1"`},
			"X-Amz-Version-Id": {"version-1"},
		}, ""), nil
	}))

	out, err := client.PutObject(context.Background(), PutObjectInput{
		Bucket:               "state-bucket",
		Key:                  "services/api/control.json",
		Body:                 []byte("release"),
		IfNoneMatch:          "*",
		ServerSideEncryption: serverSideEncryptionAWSKMS,
		SSEKMSKeyID:          "alias/skiff",
		Metadata:             map[string]string{"service": "api"},
	})
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}
	if sawBody != "release" {
		t.Fatalf("body = %q", sawBody)
	}
	if out.ETag != `"etag-1"` || out.VersionID != "version-1" {
		t.Fatalf("output = %+v", out)
	}
}

func TestHTTPClientGetHeadListAndErrorParsing(t *testing.T) {
	client := mustHTTPClientWithTransport(t, "https://s3.test", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.RawQuery == "":
			return response(http.StatusOK, http.Header{
				"ETag":               {`"etag-get"`},
				"Content-Type":       {"application/json"},
				"Content-Length":     {"7"},
				"Last-Modified":      {"Sat, 16 May 2026 18:00:00 GMT"},
				"X-Amz-Meta-Service": {"api"},
			}, "release"), nil
		case r.Method == http.MethodHead:
			return response(http.StatusOK, http.Header{
				"ETag":           {`"etag-head"`},
				"Content-Length": {"9"},
				"Last-Modified":  {"Sat, 16 May 2026 18:00:00 GMT"},
			}, ""), nil
		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			return response(http.StatusOK, http.Header{"Content-Type": {"application/xml"}}, `<ListBucketResult>
<IsTruncated>false</IsTruncated>
<Contents><Key>services/api/events/002.json</Key><LastModified>2026-05-16T18:00:01.000Z</LastModified><ETag>"b"</ETag><Size>2</Size></Contents>
<Contents><Key>services/api/events/001.json</Key><LastModified>2026-05-16T18:00:00.000Z</LastModified><ETag>"a"</ETag><Size>1</Size></Contents>
</ListBucketResult>`), nil
		case r.Method == http.MethodPut:
			return response(http.StatusPreconditionFailed, nil, `<Error><Code>PreconditionFailed</Code><Message>etag mismatch</Message></Error>`), nil
		default:
			t.Fatalf("unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			return nil, nil
		}
	}))

	got, err := client.GetObject(context.Background(), GetObjectInput{Bucket: "state-bucket", Key: "services/api/control.json"})
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	if string(got.Body) != "release" || got.ETag != `"etag-get"` || got.Metadata["service"] != "api" {
		t.Fatalf("GetObject output = %+v", got)
	}
	if got.LastModified != time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC) {
		t.Fatalf("LastModified = %s", got.LastModified)
	}

	head, err := client.HeadObject(context.Background(), HeadObjectInput{Bucket: "state-bucket", Key: "services/api/control.json"})
	if err != nil {
		t.Fatalf("HeadObject returned error: %v", err)
	}
	if head.ETag != `"etag-head"` || head.ContentLength != 9 {
		t.Fatalf("HeadObject output = %+v", head)
	}

	list, err := client.ListObjectsV2(context.Background(), ListObjectsV2Input{Bucket: "state-bucket", Prefix: "services/api/events/"})
	if err != nil {
		t.Fatalf("ListObjectsV2 returned error: %v", err)
	}
	if len(list.Objects) != 2 || list.Objects[0].Key != "services/api/events/001.json" {
		t.Fatalf("ListObjectsV2 output = %+v", list.Objects)
	}

	_, err = client.PutObject(context.Background(), PutObjectInput{Bucket: "state-bucket", Key: "services/api/control.json", Body: []byte("{}")})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("PutObject error = %T %v, want APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusPreconditionFailed || apiErr.Code != "PreconditionFailed" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestHTTPClientDeleteObject(t *testing.T) {
	var deleted bool
	client := mustHTTPClientWithTransport(t, "https://s3.test", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/state-bucket/services/api/control.json" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		deleted = true
		return response(http.StatusNoContent, nil, ""), nil
	}))

	if err := client.DeleteObject(context.Background(), "state-bucket", "services/api/control.json"); err != nil {
		t.Fatalf("DeleteObject returned error: %v", err)
	}
	if !deleted {
		t.Fatalf("server did not see delete")
	}
}

func mustHTTPClientWithTransport(t *testing.T, endpoint string, transport http.RoundTripper) *HTTPClient {
	t.Helper()
	client, err := NewHTTPClient(HTTPClientOptions{
		Region:         "us-west-2",
		Endpoint:       endpoint,
		ForcePathStyle: true,
		HTTPClient:     &http.Client{Transport: transport},
		Credentials: skiffaws.Credentials{
			AccessKeyID:     "AKIDEXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
			SessionToken:    "token",
		},
		Clock: func() time.Time {
			return time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, headers http.Header, body string) *http.Response {
	normalized := http.Header{}
	for key, values := range headers {
		for _, value := range values {
			normalized.Add(key, value)
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     normalized,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
