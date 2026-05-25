package harness

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/bootstrap"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	DefaultTraceID = "tr_harness_01"
	DefaultOwner   = "integration-harness"
)

var (
	DefaultActor = schema.Actor{ID: "integration-agent", Type: "agent"}
	DefaultNow   = time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
)

type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock {
	if now.IsZero() {
		now = DefaultNow
	}
	return &ManualClock{now: now.UTC()}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type Harness struct {
	T        testing.TB
	Ctx      context.Context
	Store    *memory.Store
	State    *state.Client
	Events   *events.Log
	Clock    *ManualClock
	Signer   *signing.LocalSigner
	Verifier *signing.LocalVerifier
}

type ReleaseFixture struct {
	Service   string
	Env       string
	ReleaseID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func New(t testing.TB) *Harness {
	t.Helper()

	store := memory.New()
	clock := NewManualClock(DefaultNow)
	stateClient := state.NewClient(
		store,
		state.WithClock(clock),
		state.WithTokenGenerator(tokenGenerator()),
		state.WithRetryOptions(state.RetryOptions{MaxAttempts: 1}),
	)
	eventLog, err := events.NewLog(events.Options{Store: store, Clock: clock.Now})
	if err != nil {
		t.Fatalf("events.NewLog returned error: %v", err)
	}
	signer, err := signing.NewLocalSignerFromSeed("integration-test", []byte(strings.Repeat("A", ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("NewLocalSignerFromSeed returned error: %v", err)
	}
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{
		signer.KeyID(): signer.PublicKey(),
	})
	if err != nil {
		t.Fatalf("NewLocalVerifier returned error: %v", err)
	}

	return &Harness{
		T:        t,
		Ctx:      context.Background(),
		Store:    store,
		State:    stateClient,
		Events:   eventLog,
		Clock:    clock,
		Signer:   signer,
		Verifier: verifier,
	}
}

func (h *Harness) LoadSpec(path string) spec.Document {
	h.T.Helper()
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		h.T.Fatalf("LoadFile(%q) returned error: %v", path, err)
	}
	return *doc
}

func (h *Harness) ValidateSpec(doc spec.Document) {
	h.T.Helper()
	if result := spec.Validate(doc); !result.OK {
		h.T.Fatalf("Validate returned diagnostics: %+v", result.Diagnostics)
	}
}

func (h *Harness) Compile(doc spec.Document) *ir.Graph {
	h.T.Helper()
	graph, err := compiler.Compile(h.Ctx, doc, compiler.Options{})
	if err != nil {
		h.T.Fatalf("Compile returned error: %v", err)
	}
	return graph
}

func (h *Harness) CreateEnvironmentRoot(env string) bootstrap.EnvironmentRoot {
	h.T.Helper()
	key, err := paths.EnvironmentRoot(env)
	if err != nil {
		h.T.Fatalf("EnvironmentRoot path returned error: %v", err)
	}
	root := bootstrap.EnvironmentRoot{
		SchemaVersion: bootstrap.EnvironmentRootSchemaVersion,
		Env:           env,
		Provider:      bootstrap.ProviderAWS,
		Region:        "us-west-2",
		StateBucket:   "memory://integration-state",
		KMSAlias:      "alias/skiff-" + env,
		Roles: map[string]string{
			"developer": "arn:aws:iam::123456789012:role/skiff-" + env + "-developer",
			"deployer":  "arn:aws:iam::123456789012:role/skiff-" + env + "-deployer",
			"runner":    "arn:aws:iam::123456789012:role/skiff-" + env + "-runner",
			"skiffd":    "arn:aws:iam::123456789012:role/skiff-" + env + "-skiffd",
		},
		CreatedAt: canonical.Time(h.Clock.Now()),
		UpdatedAt: canonical.Time(h.Clock.Now()),
	}
	h.CreateJSON(key, root)
	return root
}

func (h *Harness) CreateServiceControl(service, env string) *state.ServiceControlDocument {
	h.T.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(h.Clock.Now()), DefaultActor)
	control.TraceID = DefaultTraceID
	doc, err := h.State.CreateServiceControl(h.Ctx, control)
	if err != nil {
		h.T.Fatalf("CreateServiceControl returned error: %v", err)
	}
	return doc
}

func (h *Harness) AcquireServiceLease(service string) *state.LeaseHandle {
	h.T.Helper()
	handle, _, err := h.State.AcquireLease(h.Ctx, service, state.LeaseOptions{
		Owner:    DefaultOwner,
		Duration: 5 * time.Minute,
		Actor:    DefaultActor,
		TraceID:  DefaultTraceID,
	})
	if err != nil {
		h.T.Fatalf("AcquireLease returned error: %v", err)
	}
	return handle
}

func (h *Harness) CreateOperationIntent(service, env, operationID string) schema.OperationIntent {
	h.T.Helper()
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		h.T.Fatalf("OperationIntent path returned error: %v", err)
	}
	intent := schema.NewOperationIntent(
		operationID,
		service,
		env,
		"deploy",
		schema.Target{Kind: "service", Name: service},
		DefaultActor,
		DefaultTraceID,
		canonical.Time(h.Clock.Now()),
	)
	intent.Risk = schema.RiskMedium
	intent.Reversibility = schema.Compensatable
	intent.Summary = "deploy " + service
	h.CreateJSON(key, intent)
	return intent
}

func (h *Harness) CreateOperationControl(service, env, operationID string, status schema.OperationStatus) schema.OperationControl {
	h.T.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		h.T.Fatalf("OperationControl path returned error: %v", err)
	}
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   operationID,
		Service:       service,
		Env:           env,
		Status:        status,
		UpdatedAt:     canonical.Time(h.Clock.Now()),
		TraceID:       DefaultTraceID,
	}
	h.CreateJSON(key, control)
	return control
}

func (h *Harness) SetDesiredRelease(handle *state.LeaseHandle, releaseID, operationID string) *state.LeaseHandle {
	h.T.Helper()
	newHandle, _, err := h.State.UpdateServiceControlWithLeaseCAS(h.Ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = releaseID
		control.Operation = &schema.ActiveOperation{
			ID:    operationID,
			Kind:  "deploy",
			State: string(schema.OperationRunning),
			Step:  "desired-release-written",
		}
		control.UpdatedBy = DefaultActor
		control.TraceID = DefaultTraceID
		return nil
	})
	if err != nil {
		h.T.Fatalf("UpdateServiceControlWithLeaseCAS returned error: %v", err)
	}
	return newHandle
}

func (h *Harness) ReleaseServiceLease(handle *state.LeaseHandle) *state.ServiceControlDocument {
	h.T.Helper()
	doc, err := h.State.ReleaseLease(h.Ctx, *handle)
	if err != nil {
		h.T.Fatalf("ReleaseLease returned error: %v", err)
	}
	return doc
}

func (h *Harness) PublishSignedRelease(fixture ReleaseFixture) schema.ReleaseManifest {
	h.T.Helper()
	createdAt := fixture.CreatedAt
	if createdAt.IsZero() {
		createdAt = h.Clock.Now()
	}
	expiresAt := fixture.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = createdAt.Add(30 * 24 * time.Hour)
	}
	runtimeKey, err := paths.RuntimeManifest(fixture.Service, fixture.ReleaseID)
	if err != nil {
		h.T.Fatalf("RuntimeManifest path returned error: %v", err)
	}
	runtimeManifest := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       fixture.Service,
		Env:           fixture.Env,
		ReleaseID:     fixture.ReleaseID,
		Command:       []string{"./app", "serve"},
		EnvVars:       map[string]string{"PORT": "8080"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/healthz", Port: 8080, Interval: "10s", Timeout: "2s"},
		CreatedAt:     canonical.Time(createdAt),
	}
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		h.T.Fatalf("RuntimeManifestDigest returned error: %v", err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion: schema.Version,
		Service:       fixture.Service,
		Env:           fixture.Env,
		ReleaseID:     fixture.ReleaseID,
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "oci://registry.example.com/" + fixture.Service + "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		RuntimeManifestKey:    runtimeKey,
		RuntimeManifestDigest: runtimeDigest,
		CreatedAt:             canonical.Time(createdAt),
		ExpiresAt:             canonical.Time(expiresAt),
	}
	signed, err := release.SignManifest(h.Ctx, manifest, h.Signer, DefaultActor, h.Clock.Now())
	if err != nil {
		h.T.Fatalf("SignManifest returned error: %v", err)
	}
	releaseKey, err := paths.ReleaseManifest(fixture.Service, fixture.ReleaseID)
	if err != nil {
		h.T.Fatalf("ReleaseManifest path returned error: %v", err)
	}
	h.CreateJSON(runtimeKey, runtimeManifest)
	h.CreateJSON(releaseKey, signed)
	return signed
}

func (h *Harness) FetchRelease(service, env, releaseID string) *release.FetchedRelease {
	h.T.Helper()
	fetched, err := release.Fetch(h.Ctx, h.Store, release.FetchOptions{
		Service:   service,
		Env:       env,
		ReleaseID: releaseID,
		Verifier:  h.Verifier,
		Now:       h.Clock.Now(),
	})
	if err != nil {
		h.T.Fatalf("Fetch returned error: %v", err)
	}
	return fetched
}

func (h *Harness) AppendOperationEvent(service, operationID, eventType, summary string) events.Event {
	h.T.Helper()
	event := events.NewOperationEvent(service, operationID, eventType, summary, h.Clock.Now(), DefaultTraceID+eventType)
	event.TraceID = DefaultTraceID
	event.Actor = &DefaultActor
	if _, err := h.Events.Append(h.Ctx, event); err != nil {
		h.T.Fatalf("Append operation event returned error: %v", err)
	}
	return event
}

func (h *Harness) CreateJSON(key string, document any) {
	h.T.Helper()
	body, err := canonical.Marshal(document)
	if err != nil {
		h.T.Fatalf("Marshal(%s) returned error: %v", key, err)
	}
	if _, err := h.Store.Create(h.Ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		h.T.Fatalf("Create(%s) returned error: %v", key, err)
	}
}

func (h *Harness) ObjectKeys(prefix string) []string {
	h.T.Helper()
	metas, err := h.Store.List(h.Ctx, prefix, objstore.ListOptions{})
	if err != nil {
		h.T.Fatalf("List(%q) returned error: %v", prefix, err)
	}
	keys := make([]string, 0, len(metas))
	for _, meta := range metas {
		keys = append(keys, meta.Key)
	}
	sort.Strings(keys)
	return keys
}

func (h *Harness) AssertObjectExists(key string) {
	h.T.Helper()
	if _, err := h.Store.Head(h.Ctx, key); err != nil {
		h.T.Fatalf("Head(%s) returned error: %v", key, err)
	}
}

func (h *Harness) AssertJSONEqual(key string, want any) {
	h.T.Helper()
	object, err := h.Store.Get(h.Ctx, key)
	if err != nil {
		h.T.Fatalf("Get(%s) returned error: %v", key, err)
	}
	wantBody, err := canonical.Marshal(want)
	if err != nil {
		h.T.Fatalf("Marshal want for %s returned error: %v", key, err)
	}
	if string(object.Body) != string(wantBody) {
		h.T.Fatalf("%s JSON mismatch\n got: %s\nwant: %s", key, string(object.Body), string(wantBody))
	}
}

func (h *Harness) AssertObjectMatchesGolden(key, goldenPath string) {
	h.T.Helper()
	object, err := h.Store.Get(h.Ctx, key)
	if err != nil {
		h.T.Fatalf("Get(%s) returned error: %v", key, err)
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		h.T.Fatalf("ReadFile(%s) returned error: %v", goldenPath, err)
	}
	want = bytes.TrimSpace(want)
	if string(object.Body) != string(want) {
		h.T.Fatalf("%s JSON mismatch\n got: %s\nwant: %s", key, string(object.Body), string(want))
	}
}

func (h *Harness) DecodeJSON(key string, target any) {
	h.T.Helper()
	object, err := h.Store.Get(h.Ctx, key)
	if err != nil {
		h.T.Fatalf("Get(%s) returned error: %v", key, err)
	}
	if err := canonical.UnmarshalStrict(object.Body, target); err != nil {
		h.T.Fatalf("UnmarshalStrict(%s) returned error: %v", key, err)
	}
}

func (h *Harness) PrettyJSON(key string) string {
	h.T.Helper()
	object, err := h.Store.Get(h.Ctx, key)
	if err != nil {
		h.T.Fatalf("Get(%s) returned error: %v", key, err)
	}
	var value any
	if err := json.Unmarshal(object.Body, &value); err != nil {
		h.T.Fatalf("Unmarshal(%s) returned error: %v", key, err)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		h.T.Fatalf("MarshalIndent(%s) returned error: %v", key, err)
	}
	return string(body)
}

func IsLeaseHeld(err error) bool {
	return errors.Is(err, state.ErrLeaseHeld)
}

func ServiceControlPath(t testing.TB, service string) string {
	t.Helper()
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatalf("ServiceControl path returned error: %v", err)
	}
	return key
}

func EnvironmentRootPath(t testing.TB, env string) string {
	t.Helper()
	key, err := paths.EnvironmentRoot(env)
	if err != nil {
		t.Fatalf("EnvironmentRoot path returned error: %v", err)
	}
	return key
}

func OperationIntentPath(t testing.TB, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		t.Fatalf("OperationIntent path returned error: %v", err)
	}
	return key
}

func OperationControlPath(t testing.TB, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatalf("OperationControl path returned error: %v", err)
	}
	return key
}

func ReleaseManifestPath(t testing.TB, service, releaseID string) string {
	t.Helper()
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatalf("ReleaseManifest path returned error: %v", err)
	}
	return key
}

func RuntimeManifestPath(t testing.TB, service, releaseID string) string {
	t.Helper()
	key, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatalf("RuntimeManifest path returned error: %v", err)
	}
	return key
}

func OperationEventPath(t testing.TB, service, operationID, eventID string) string {
	t.Helper()
	key, err := paths.OperationEvent(service, operationID, eventID)
	if err != nil {
		t.Fatalf("OperationEvent path returned error: %v", err)
	}
	return key
}

func tokenGenerator() state.TokenGenerator {
	var mu sync.Mutex
	var next int
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		next++
		return fmt.Sprintf("lease_%06d", next)
	}
}
