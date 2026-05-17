package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRotateSecretJSONCreatesSagaAndStopsBeforePromotionApproval(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	oldProvider := newRotateProvider
	newRotateProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return fakeprovider.New(fakeprovider.WithStateStore(store)), nil
	}
	t.Cleanup(func() { newRotateProvider = oldProvider })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://managed-database/orders-db/connection-url",
		"--consumers", "orders-api,orders-worker",
		"--canary-consumer", "orders-api",
		"--database", "orders-db",
		"--disable-after", "48h",
		"--direct",
		"--state", "file://" + dir,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("rotate output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_rotate_cli" || got.Result.Status != schema.SagaRunning || got.Result.NextAction != "approve_or_reject" {
		t.Fatalf("unexpected rotate output: %+v", got)
	}
	if len(got.Result.CurrentSteps) != 1 || got.Result.CurrentSteps[0] != "approve-promotion" {
		t.Fatalf("rotation should wait at promotion approval: %+v", got.Result.CurrentSteps)
	}
}

func TestRotateSecretDryRunJSONIncludesApprovalAndDelayedDisable(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://app/api-key",
		"--consumers", "orders-api,orders-worker",
		"--dry-run",
		"--direct",
		"--state", "memory://rotate-dry-run",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Plan == nil {
		t.Fatalf("dry-run did not include plan")
	}
	var sawCanary, sawApproval, sawDelayedDisable bool
	for _, node := range got.Result.Plan.Graph.Nodes {
		switch node.ID {
		case "canary-consumer":
			sawCanary = node.Kind == "service.canary_with_secret"
		case "approve-promotion":
			sawApproval = node.Kind == "approval.manual"
		case "schedule-disable-old":
			sawDelayedDisable = node.Kind == "credential.disable_old"
		}
	}
	if !sawCanary || !sawApproval || !sawDelayedDisable {
		t.Fatalf("rotation plan missing canary, approval, or delayed disable: %+v", got.Result.Plan.Graph.Nodes)
	}
}

func TestRotateSecretProductionRequiresApproval(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://app/api-key",
		"--consumers", "orders-api",
		"--direct",
		"--state", "memory://rotate-prod-approval",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_prod",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d; stderr = %s stdout = %s", code, ExitUserError, stderr.String(), stdout.String())
	}
	var got commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || !strings.Contains(got.Summary, "approval required") {
		t.Fatalf("unexpected approval error: %+v", got)
	}
}

func TestRotateKeyDryRunJSONShowsBlastRadiusAndApproval(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "key", "alias/skiff/prod/state",
		"--consumers", "payments-api,orders-api",
		"--canary-consumer", "payments-api",
		"--material-refs", "secret://payments/api-token",
		"--disable-after", "240h",
		"--dry-run",
		"--direct",
		"--state", "memory://rotate-key-dry-run",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_key_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Command != "key" || got.Result.KeyAlias != "alias/skiff/prod/state" || got.Result.Reversibility != schema.PartiallyReversible {
		t.Fatalf("unexpected key rotation result: %+v", got.Result)
	}
	if !containsRotateValue(got.Result.BlastRadius, "consumer:payments-api") || !containsRotateValue(got.Result.BlastRadius, "material_ref:secret://payments/api-token") {
		t.Fatalf("key rotation missing blast radius: %+v", got.Result.BlastRadius)
	}
	if got.Result.Plan == nil {
		t.Fatalf("dry-run did not include plan")
	}
	var sawApproval, sawScheduleDisable bool
	for _, node := range got.Result.Plan.Graph.Nodes {
		if node.ID == "approve-promotion" && node.Kind == "approval.manual" {
			sawApproval = true
		}
		if node.ID == "schedule-disable-old-key" && node.Kind == "key.schedule_disable_old" {
			sawScheduleDisable = true
		}
	}
	if !sawApproval || !sawScheduleDisable {
		t.Fatalf("key rotation plan missing approval or delayed disable: %+v", got.Result.Plan.Graph.Nodes)
	}
}

func TestRotateCertificateDryRunJSONShowsConsumerVerification(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "cert", "payments-api-mtls",
		"--certificate-ref", "aws-acm://us-west-2/certificate/payments-api",
		"--trust-store-ref", "aws-acm-pca://us-west-2/private-ca/root",
		"--consumers", "payments-api,orders-api",
		"--canary-consumer", "payments-api",
		"--retire-after", "240h",
		"--dry-run",
		"--direct",
		"--state", "memory://rotate-cert-dry-run",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_cert_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Command != "cert" || got.Result.Certificate != "payments-api-mtls" || got.Result.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected certificate rotation result: %+v", got.Result)
	}
	if !containsRotateValue(got.Result.BlastRadius, "trust_store_ref:aws-acm-pca://us-west-2/private-ca/root") {
		t.Fatalf("certificate rotation missing blast radius: %+v", got.Result.BlastRadius)
	}
	if got.Result.Plan == nil {
		t.Fatalf("dry-run did not include plan")
	}
	var sawVerify, sawRetire bool
	for _, node := range got.Result.Plan.Graph.Nodes {
		if node.ID == "verify-consumer-trust" && node.Kind == "certificate.verify_consumer_trust" {
			sawVerify = true
		}
		if node.ID == "schedule-retire-old-certificate" && node.Kind == "certificate.schedule_retire_old" {
			sawRetire = true
		}
	}
	if !sawVerify || !sawRetire {
		t.Fatalf("certificate rotation plan missing trust verification or delayed retire: %+v", got.Result.Plan.Graph.Nodes)
	}
}

func TestRotateKeyWithApprovalCreatesPendingSaga(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "key", "alias/skiff/prod/state",
		"--consumers", "payments-api",
		"--material-refs", "secret://payments/api-token",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--approval-id", "approval_key_rotation",
		"--format", "json",
		"--trace-id", "tr_rotate_key_create",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Command != "key" || got.Result.Status != schema.SagaPending || got.Result.NextAction != "resume" {
		t.Fatalf("unexpected key rotation create result: %+v", got.Result)
	}
	if got.Result.Plan != nil {
		t.Fatalf("non-dry-run output should not embed plan: %+v", got.Result.Plan)
	}
}

func TestRotateKeyProductionRequiresApproval(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "key", "alias/skiff/prod/state",
		"--consumers", "payments-api",
		"--direct",
		"--state", "memory://rotate-key-prod-approval",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_key_prod",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d; stderr = %s stdout = %s", code, ExitUserError, stderr.String(), stdout.String())
	}
	var got commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || !strings.Contains(got.Summary, "approval required") {
		t.Fatalf("unexpected approval error: %+v", got)
	}
}

func containsRotateValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
