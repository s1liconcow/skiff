package schema_test

import (
	"reflect"
	"testing"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDurableSchemasCarrySchemaVersion(t *testing.T) {
	for _, doc := range []any{
		schema.ServiceControl{},
		schema.OperationIntent{},
		schema.OperationControl{},
		schema.ReleaseManifest{},
		schema.ReleaseCandidate{},
		schema.RuntimeManifest{},
		schema.SagaIntent{},
		schema.SagaGraph{},
		schema.SagaControl{},
		schema.ResourceRecord{},
		schema.Event{},
		schema.AuditRecord{},
		schema.ServicesIndex{},
		schema.ActiveSagasIndex{},
		schema.RecentEventsIndex{},
		schema.ServiceObservation{},
	} {
		t.Run(reflect.TypeOf(doc).Name(), func(t *testing.T) {
			field, ok := reflect.TypeOf(doc).FieldByName("SchemaVersion")
			if !ok {
				t.Fatalf("missing SchemaVersion field")
			}
			if field.Tag.Get("json") != "schema_version" {
				t.Fatalf("SchemaVersion JSON tag = %q, want schema_version", field.Tag.Get("json"))
			}
		})
	}
}

func TestConstructorsSetSchemaVersion(t *testing.T) {
	actor := schema.Actor{ID: "alpha-one", Type: "agent"}
	service := schema.NewServiceControl("payments-api", "prod", "2026-05-16T17:00:00Z", actor)
	if service.SchemaVersion != schema.Version {
		t.Fatalf("service schema version = %q", service.SchemaVersion)
	}

	intent := schema.NewOperationIntent(
		"op_01JABC",
		"payments-api",
		"prod",
		"deploy",
		schema.Target{Kind: "service", Name: "payments-api"},
		actor,
		"tr_01JABC",
		"2026-05-16T17:00:00Z",
	)
	if intent.SchemaVersion != schema.Version {
		t.Fatalf("operation intent schema version = %q", intent.SchemaVersion)
	}

	control := schema.NewSagaControl("saga_01JABC", schema.SagaPending, "2026-05-16T17:00:00Z")
	if control.SchemaVersion != schema.Version {
		t.Fatalf("saga control schema version = %q", control.SchemaVersion)
	}
}
