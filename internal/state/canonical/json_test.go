package canonical_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestMarshalStableJSON(t *testing.T) {
	doc := schema.NewServiceControl("payments-api", "prod-us-west-2", "2026-05-16T17:00:00Z", schema.Actor{
		ID:   "alpha-one",
		Type: "agent",
	})

	first, err := canonical.MarshalString(doc)
	if err != nil {
		t.Fatalf("MarshalString returned error: %v", err)
	}
	second, err := canonical.MarshalString(doc)
	if err != nil {
		t.Fatalf("MarshalString second call returned error: %v", err)
	}
	if first != second {
		t.Fatalf("canonical JSON was not stable:\nfirst:  %s\nsecond: %s", first, second)
	}
	if strings.Contains(first, "\n") {
		t.Fatalf("canonical JSON should be single-line, got %q", first)
	}
	wantPrefix := `{"schema_version":"skiff.state/v1","service":"payments-api","env":"prod-us-west-2"`
	if !strings.HasPrefix(first, wantPrefix) {
		t.Fatalf("canonical JSON = %s, want prefix %s", first, wantPrefix)
	}
}

func TestUnmarshalStrictRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	var doc schema.ServiceControl
	if err := canonical.UnmarshalStrict([]byte(`{"schema_version":"skiff.state/v1","service":"payments-api","env":"prod","updated_at":"2026-05-16T17:00:00Z","updated_by":{"id":"alpha-one","type":"agent"},"extra":true}`), &doc); err == nil {
		t.Fatalf("UnmarshalStrict accepted unknown field")
	}
	if err := canonical.UnmarshalStrict([]byte(`{"schema_version":"skiff.state/v1","service":"payments-api","env":"prod","updated_at":"2026-05-16T17:00:00Z","updated_by":{"id":"alpha-one","type":"agent"}} {}`), &doc); err == nil {
		t.Fatalf("UnmarshalStrict accepted trailing JSON value")
	}
}

func TestCanonicalTimeUsesUTC(t *testing.T) {
	local := time.Date(2026, 5, 16, 10, 30, 0, 123, time.FixedZone("test", -7*60*60))
	if got, want := canonical.Time(local), "2026-05-16T17:30:00.000000123Z"; got != want {
		t.Fatalf("Time = %q, want %q", got, want)
	}
}

func TestGoldenDurableObjects(t *testing.T) {
	actor := schema.Actor{ID: "alpha-one", Type: "agent"}
	tests := []struct {
		name   string
		doc    any
		golden string
	}{
		{
			name: "service control",
			doc: schema.ServiceControl{
				SchemaVersion:  schema.Version,
				Service:        "payments-api",
				Env:            "prod",
				DesiredRelease: "rel_01JABC",
				StableRelease:  "rel_01JAAA",
				Version:        1,
				UpdatedAt:      "2026-05-16T17:00:00Z",
				UpdatedBy:      actor,
				TraceID:        "tr_01JABC",
			},
			golden: "testdata/service_control.golden.json",
		},
		{
			name: "release manifest",
			doc: schema.ReleaseManifest{
				SchemaVersion: schema.Version,
				Service:       "payments-api",
				Env:           "prod",
				ReleaseID:     "rel_01JABC",
				Artifact: schema.ArtifactRef{
					Type:   "binary",
					URI:    "s3://skiff-artifacts-prod/payments-api/rel_01JABC/app",
					Digest: "sha256:abc123",
				},
				RuntimeManifestKey: "services/payments-api/releases/rel_01JABC/runtime-manifest.json",
				Digest:             "sha256:def456",
				CreatedAt:          "2026-05-16T17:00:00Z",
				ExpiresAt:          "2026-06-16T17:00:00Z",
				Signatures: []schema.Signature{
					{KeyID: "local-test", Algorithm: "ed25519", Signature: "sig_test", SignedAt: "2026-05-16T17:00:01Z"},
				},
			},
			golden: "testdata/release_manifest.golden.json",
		},
		{
			name: "saga control",
			doc: schema.SagaControl{
				SchemaVersion: schema.Version,
				SagaID:        "saga_01JABC",
				Status:        schema.SagaRunning,
				CurrentSteps:  []string{"plan", "apply"},
				UpdatedAt:     "2026-05-16T17:00:00Z",
				TraceID:       "tr_01JABC",
			},
			golden: "testdata/saga_control.golden.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonical.Marshal(tc.doc)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			want, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			want = []byte(strings.TrimSpace(string(want)))
			if string(got) != string(want) {
				t.Fatalf("canonical JSON mismatch\ngot:  %s\nwant: %s", got, want)
			}
		})
	}
}
