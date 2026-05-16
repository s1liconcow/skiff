package deploy_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDeployDryRunPlansAndWritesNothing(t *testing.T) {
	store := memory.New()
	p := newAWSProviderForDeploy(t, nil, nil)
	result, err := deploy.Deployer{Provider: p}.Deploy(context.Background(), compileDeployGraph(t), deploy.Request{
		DryRun:  true,
		Actor:   schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID: "tr_deploy_dry",
	})
	if err != nil {
		t.Fatalf("dry-run deploy: %v", err)
	}
	if !result.OK || !result.DryRun || result.Plan == nil || len(result.Plan.Resources) == 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	objects, err := store.List(context.Background(), "", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatalf("dry-run wrote state objects: %+v", objects)
	}
}

func TestDeployPublishesReleaseUpdatesControlAndEvents(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	manager := &fakeServiceResourceManager{}
	signer := testSigner(t)
	p := newAWSProviderForDeploy(t, manager, store)
	graph := compileDeployGraph(t)
	result, err := deploy.Deployer{
		Store:    store,
		Provider: p,
		Signer:   signer,
		Clock:    fixedDeployNow,
	}.Deploy(ctx, graph, deploy.Request{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_deploy",
		ReleaseID:   "rel_01JDEPLOY",
		OperationID: "op_01JDEPLOY",
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !result.OK || result.ReleaseID != "rel_01JDEPLOY" || result.OperationID != "op_01JDEPLOY" {
		t.Fatalf("unexpected deploy result: %+v", result)
	}
	if manager.applyCalls != len(result.Plan.Resources) {
		t.Fatalf("apply calls = %d, want %d", manager.applyCalls, len(result.Plan.Resources))
	}

	runtimeKey, err := paths.RuntimeManifest(graph.Service, result.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	releaseKey, err := paths.ReleaseManifest(graph.Service, result.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest := readObject[schema.RuntimeManifest](t, store, runtimeKey)
	releaseManifest := readObject[schema.ReleaseManifest](t, store, releaseKey)
	if runtimeManifest.Metrics == nil || !runtimeManifest.Metrics.Enabled || runtimeManifest.Metrics.Path != "/metrics" {
		t.Fatalf("runtime manifest missing metrics config: %+v", runtimeManifest.Metrics)
	}
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{signer.KeyID(): signer.PublicKey()})
	if err != nil {
		t.Fatal(err)
	}
	verification := release.VerifyManifest(ctx, releaseManifest, release.VerifyOptions{
		Service:         graph.Service,
		Env:             graph.Env,
		ReleaseID:       result.ReleaseID,
		RuntimeManifest: &runtimeManifest,
		Verifier:        verifier,
		Now:             fixedDeployNow(),
	})
	if !verification.OK {
		t.Fatalf("published release did not verify: %+v", verification.Findings)
	}

	control, err := state.NewClient(store).GetServiceControl(ctx, graph.Service)
	if err != nil {
		t.Fatalf("service control: %v", err)
	}
	if control.Control.DesiredRelease != result.ReleaseID || control.Control.Lease != nil {
		t.Fatalf("service control not updated/released: %+v", control.Control)
	}
	if control.Control.Operation == nil || control.Control.Operation.ID != result.OperationID {
		t.Fatalf("service control missing operation: %+v", control.Control.Operation)
	}
	opControlKey, err := paths.OperationControl(graph.Service, result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	opControl := readObject[schema.OperationControl](t, store, opControlKey)
	if opControl.Status != schema.OperationSucceeded {
		t.Fatalf("operation control status = %s", opControl.Status)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: fixedDeployNow})
	if err != nil {
		t.Fatal(err)
	}
	operationEvents, err := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: graph.Service, Operation: result.OperationID}, events.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(operationEvents) < 4 {
		t.Fatalf("operation events = %d, want at least 4", len(operationEvents))
	}
	auditObjects, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(auditObjects) != 1 {
		t.Fatalf("audit object count = %d, want 1", len(auditObjects))
	}
}

func TestDeployLeaseHeldDoesNotPublishRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	graph := compileDeployGraph(t)
	client := state.NewClient(store, state.WithClock(testClock{now: fixedDeployNow()}))
	_, err := client.CreateServiceControl(ctx, schema.NewServiceControl(graph.Service, graph.Env, canonical.Time(fixedDeployNow()), schema.Actor{ID: "seed", Type: "agent"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.AcquireLease(ctx, graph.Service, state.LeaseOptions{Owner: "other-agent", Duration: time.Minute, Actor: schema.Actor{ID: "other-agent", Type: "agent"}}); err != nil {
		t.Fatal(err)
	}

	_, err = deploy.Deployer{
		Store:    store,
		Provider: newAWSProviderForDeploy(t, &fakeServiceResourceManager{}, store),
		Signer:   testSigner(t),
		Clock:    fixedDeployNow,
	}.Deploy(ctx, graph, deploy.Request{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_lease",
		ReleaseID:   "rel_01JLEASE",
		OperationID: "op_01JLEASE",
	})
	if err == nil || !strings.Contains(err.Error(), "LEASE_HELD") {
		t.Fatalf("deploy err = %v, want lease held", err)
	}
	releaseKey, err := paths.ReleaseManifest(graph.Service, "rel_01JLEASE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, releaseKey); err == nil {
		t.Fatalf("release was published despite held lease")
	}
}

func TestDeployProviderApplyFailureKeepsReleaseHistoryAndMarksOperationFailed(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	graph := compileDeployGraph(t)
	result, err := deploy.Deployer{
		Store:    store,
		Provider: newAWSProviderForDeploy(t, &fakeServiceResourceManager{applyErr: errors.New("provider apply failed")}, store),
		Signer:   testSigner(t),
		Clock:    fixedDeployNow,
	}.Deploy(ctx, graph, deploy.Request{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_apply_failed",
		ReleaseID:   "rel_01JFAILED",
		OperationID: "op_01JFAILED",
	})
	if err == nil || result == nil || result.OK {
		t.Fatalf("deploy result=%+v err=%v, want failed result", result, err)
	}
	releaseKey, err := paths.ReleaseManifest(graph.Service, "rel_01JFAILED")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, releaseKey); err != nil {
		t.Fatalf("failed deploy should leave immutable release history: %v", err)
	}
	control, err := state.NewClient(store).GetServiceControl(ctx, graph.Service)
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.DesiredRelease == "rel_01JFAILED" {
		t.Fatalf("desired release changed despite provider failure")
	}
	opControlKey, err := paths.OperationControl(graph.Service, "op_01JFAILED")
	if err != nil {
		t.Fatal(err)
	}
	opControl := readObject[schema.OperationControl](t, store, opControlKey)
	if opControl.Status != schema.OperationFailed {
		t.Fatalf("operation status = %s, want failed", opControl.Status)
	}
}

func compileDeployGraph(t *testing.T) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(deployTestSpec), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return graph
}

func newAWSProviderForDeploy(t *testing.T, manager *fakeServiceResourceManager, store *memory.Store) *aws.Provider {
	t.Helper()
	opts := []aws.Option{}
	if manager != nil {
		opts = append(opts, aws.WithClients(aws.Clients{ServiceResources: manager}))
	}
	if store != nil {
		opts = append(opts, aws.WithStateStore(store))
	}
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeServiceResourceManager struct {
	applyCalls int
	applyErr   error
}

func (m *fakeServiceResourceManager) PlanResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.ResourcePlan, error) {
	return &aws.ResourcePlan{Action: provider.ActionCreate, Summary: "create " + desired.Summary}, nil
}

func (m *fakeServiceResourceManager) ApplyResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.AppliedResource, error) {
	m.applyCalls++
	if m.applyErr != nil {
		return nil, m.applyErr
	}
	return &aws.AppliedResource{
		Kind:       desired.Kind,
		LogicalID:  desired.LogicalID,
		Name:       desired.Name,
		ProviderID: desired.Kind + "/" + desired.Name,
		Status:     "applied",
		Tags:       desired.Tags,
	}, nil
}

func testSigner(t *testing.T) *signing.LocalSigner {
	t.Helper()
	signer, err := signing.NewLocalSignerFromSeed("deploy-test", []byte(strings.Repeat("D", ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func readObject[T any](t *testing.T, store *memory.Store, key string) T {
	t.Helper()
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	var out T
	if err := canonical.UnmarshalStrict(obj.Body, &out); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return out
}

func fixedDeployNow() time.Time {
	return time.Date(2026, 5, 16, 22, 45, 0, 0, time.UTC)
}

type testClock struct {
	now time.Time
}

func (c testClock) Now() time.Time {
	return c.now
}

const deployTestSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 2
  max: 4
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
`
