package paths_test

import (
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/state/paths"
)

func TestCoreObjectPaths(t *testing.T) {
	tests := []struct {
		name string
		path func() (string, error)
		want string
	}{
		{
			name: "service control",
			path: func() (string, error) {
				return paths.ServiceControl("payments-api")
			},
			want: "services/payments-api/control.json",
		},
		{
			name: "release manifest",
			path: func() (string, error) {
				return paths.ReleaseManifest("payments-api", "rel_01JABC")
			},
			want: "services/payments-api/releases/rel_01JABC/release.json",
		},
		{
			name: "runtime manifest",
			path: func() (string, error) {
				return paths.RuntimeManifest("payments-api", "rel_01JABC")
			},
			want: "services/payments-api/releases/rel_01JABC/runtime-manifest.json",
		},
		{
			name: "operation event",
			path: func() (string, error) {
				return paths.OperationEvent("payments-api", "op_01JABC", "01JABCDEF")
			},
			want: "services/payments-api/operations/op_01JABC/events/01JABCDEF.json",
		},
		{
			name: "saga graph",
			path: func() (string, error) {
				return paths.SagaGraph("saga_01JABC")
			},
			want: "sagas/saga_01JABC/graph.json",
		},
		{
			name: "saga artifact",
			path: func() (string, error) {
				return paths.SagaArtifact("saga_01JABC", "plans/shift-traffic.json")
			},
			want: "sagas/saga_01JABC/artifacts/plans/shift-traffic.json",
		},
		{
			name: "saga step result",
			path: func() (string, error) {
				return paths.SagaStepResult("saga_01JABC", "shift-traffic")
			},
			want: "sagas/saga_01JABC/artifacts/results/shift-traffic.json",
		},
		{
			name: "logical resource",
			path: func() (string, error) {
				return paths.LogicalResource("target-group", "payments-api")
			},
			want: "resources/by-logical/target-group/payments-api.json",
		},
		{
			name: "provider resource escapes separators",
			path: func() (string, error) {
				return paths.ProviderResource("aws", "target-group", "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/payments/abc")
			},
			want: "resources/by-provider/aws/target-group/arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup%2Fpayments%2Fabc.json",
		},
		{
			name: "service log archive prefix",
			path: func() (string, error) {
				return paths.ServiceLogArchivePrefix("payments-api", "prod")
			},
			want: "services/payments-api/log-archives/prod/",
		},
		{
			name: "audit event for UTC time",
			path: func() (string, error) {
				return paths.AuditEventForTime(time.Date(2026, 5, 16, 23, 0, 0, 0, time.FixedZone("west", -7*60*60)), "01JABCDEF")
			},
			want: "audit/2026-05-17/01JABCDEF.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.path()
			if err != nil {
				t.Fatalf("path returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathInputValidation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "rejects underscore service",
			err:  serviceControlErr("payments_api"),
			code: "INVALID_NAME",
		},
		{
			name: "rejects path separator",
			err:  serviceControlErr("payments/api"),
			code: "INVALID_NAME",
		},
		{
			name: "rejects reserved segment",
			err:  serviceControlErr("services"),
			code: "RESERVED_NAME",
		},
		{
			name: "rejects relative event id",
			err:  sagaEventErr("saga_01JABC", ".."),
			code: "INVALID_ID",
		},
		{
			name: "rejects relative saga artifact",
			err:  sagaArtifactErr("saga_01JABC", "../secret.json"),
			code: "INVALID_ID",
		},
		{
			name: "rejects invalid audit date",
			err:  auditEventErr("2026-5-16", "01JABCDEF"),
			code: "INVALID_DATE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var inputErr paths.InputError
			if !errors.As(tc.err, &inputErr) {
				t.Fatalf("error = %v, want InputError", tc.err)
			}
			if inputErr.Code != tc.code {
				t.Fatalf("code = %q, want %q", inputErr.Code, tc.code)
			}
		})
	}
}

func TestEnvironmentNameValidation(t *testing.T) {
	for _, env := range []string{"prod", "prod-us-west-2", "dev2"} {
		if err := paths.ValidateName("env", env); err != nil {
			t.Fatalf("ValidateName(%q) returned error: %v", env, err)
		}
	}
	for _, env := range []string{"Prod", "prod_us_west_2", "-prod", "prod-"} {
		if err := paths.ValidateName("env", env); err == nil {
			t.Fatalf("ValidateName(%q) returned nil, want error", env)
		}
	}
}

func serviceControlErr(service string) error {
	_, err := paths.ServiceControl(service)
	return err
}

func sagaEventErr(saga, event string) error {
	_, err := paths.SagaEvent(saga, event)
	return err
}

func sagaArtifactErr(saga, artifact string) error {
	_, err := paths.SagaArtifact(saga, artifact)
	return err
}

func auditEventErr(day, event string) error {
	_, err := paths.AuditEvent(day, event)
	return err
}
