package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	internalpackages "github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func TestCompileServiceGoldenIR(t *testing.T) {
	graph := compileExample(t)
	body, err := canonical.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	goldenPath := filepath.Join("..", "golden", "compiler", "service-ir.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(want)) {
		t.Fatalf("IR golden mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(body))
	}
	if strings.Contains(string(body), "arn:") {
		t.Fatalf("provider-specific ARN leaked into common IR:\n%s", string(body))
	}
}

func TestCompileServiceResourceMetadata(t *testing.T) {
	graph := compileExample(t)
	if len(graph.Resources.RuntimeManifests) != 1 || !graph.Resources.RuntimeManifests[0].Metrics.Enabled || graph.Resources.RuntimeManifests[0].Metrics.Path != "/metrics" {
		t.Fatalf("runtime manifest missing app metrics endpoint: %+v", graph.Resources.RuntimeManifests)
	}
	for _, meta := range resourceMetas(graph) {
		if meta.LogicalID == "" || meta.Kind == "" || meta.Name == "" {
			t.Fatalf("resource metadata missing identity: %+v", meta)
		}
		for _, tag := range []string{ir.TagService, ir.TagEnv, ir.TagManaged, ir.TagGraph} {
			if meta.Tags[tag] == "" {
				t.Fatalf("%s missing required tag %s: %+v", meta.LogicalID, tag, meta.Tags)
			}
		}
		if len(meta.Source) == 0 {
			t.Fatalf("%s missing source refs", meta.LogicalID)
		}
	}
}

func TestCompileStatefulGroupJetStreamIR(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml")
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load stateful example spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile stateful example spec: %v", err)
	}
	if graph.Service != "orders-stream" || graph.Env != "prod" {
		t.Fatalf("graph target = %s/%s, want prod/orders-stream", graph.Env, graph.Service)
	}
	if len(graph.Resources.StatefulGroups) != 1 || len(graph.Resources.StatefulMembers) != 3 || len(graph.Resources.StatefulVolumes) != 3 || len(graph.Resources.StatefulDNS) != 3 {
		t.Fatalf("stateful resources missing: %+v", graph.Resources)
	}
	if len(graph.Resources.StatefulRecipes) != 1 || len(graph.Resources.SnapshotPolicies) != 1 || len(graph.Resources.UpdatePolicies) != 1 {
		t.Fatalf("stateful policy resources missing: %+v", graph.Resources)
	}
	recipe := graph.Resources.StatefulRecipes[0]
	if recipe.Name != "nats-jetstream" || recipe.Artifact.Ref == "" || recipe.HealthCheck.Port != 8222 || !recipe.Metrics.Enabled {
		t.Fatalf("recipe runtime not compiled: %+v", recipe)
	}
	snapshot := graph.Resources.SnapshotPolicies[0]
	if !snapshot.Enabled || snapshot.Interval != "15m" || snapshot.Retention != "7d" {
		t.Fatalf("snapshot policy = %+v", snapshot)
	}
	if graph.Resources.UpdatePolicies[0].Strategy != "ordered" {
		t.Fatalf("update policy = %+v", graph.Resources.UpdatePolicies[0])
	}
	body, err := canonical.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal stateful IR: %v", err)
	}
	if strings.Contains(string(body), "arn:") {
		t.Fatalf("provider-specific ARN leaked into common IR:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"routes":["nats://orders-stream-0.state.prod.internal.example.com:6222"`) {
		t.Fatalf("recipe routes were not preserved as string URLs:\n%s", string(body))
	}
	for _, meta := range resourceMetas(graph) {
		for _, tag := range []string{ir.TagService, ir.TagEnv, ir.TagManaged, ir.TagGraph, ir.TagStatefulGroup} {
			if meta.Tags[tag] == "" {
				t.Fatalf("%s missing stateful tag %s: %+v", meta.LogicalID, tag, meta.Tags)
			}
		}
		if meta.Kind == ir.ResourceKindStatefulMember || meta.Kind == ir.ResourceKindStatefulVolume || meta.Kind == ir.ResourceKindStatefulDNS {
			if meta.Tags[ir.TagMemberOrdinal] == "" {
				t.Fatalf("%s missing member ordinal tag: %+v", meta.LogicalID, meta.Tags)
			}
		}
		if len(meta.Source) == 0 {
			t.Fatalf("%s missing source refs", meta.LogicalID)
		}
	}
}

func TestCompileStatefulGroupMinimalGoldenIR(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: ledger
  env: dev
stateful:
  replicas: 1
  volume:
    size: 10Gi
  recipe:
    name: sqlite
    config:
      artifact:
        type: oci
        ref: registry.example.com/ledger@sha256:abc123
      runtime:
        command:
          - /bin/ledger
        ports:
          client: 9000
        health:
          path: /healthz
          port: 9000
  update:
    strategy: ordered
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	body, err := canonical.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal stateful IR: %v", err)
	}
	const want = `{"schema_version":"skiff.ir/v1alpha1","service":"ledger","env":"dev","resources":{"stateful_groups":[{"meta":{"logical_id":"stateful-group:ledger","kind":"StatefulGroup","name":"skiff-dev-ledger-stateful-group","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.metadata"},{"path":"$.stateful"}]},"replicas":1,"member_refs":["stateful-member:ledger:0"],"volume_refs":["stateful-volume:ledger:0"],"dns_identity_refs":["stateful-dns:ledger:0"],"recipe_runtime_ref":"stateful-recipe:ledger","snapshot_policy_ref":"stateful-snapshot-policy:ledger","update_policy_ref":"stateful-update-policy:ledger"}],"stateful_members":[{"meta":{"logical_id":"stateful-member:ledger:0","kind":"StatefulMember","name":"skiff-dev-ledger-member-0","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/member-ordinal":"0","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.replicas"},{"path":"$.stateful.recipe"},{"path":"$.stateful.update"}]},"ordinal":0,"dns_name":"ledger-0","volume_ref":"stateful-volume:ledger:0","dns_identity_ref":"stateful-dns:ledger:0","recipe_runtime_ref":"stateful-recipe:ledger","update_policy_ref":"stateful-update-policy:ledger"}],"stateful_volumes":[{"meta":{"logical_id":"stateful-volume:ledger:0","kind":"StatefulVolume","name":"skiff-dev-ledger-volume-0","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/member-ordinal":"0","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.volume"},{"path":"$.stateful.replicas"}]},"member_ordinal":0,"size":"10Gi","type":"gp3","mount_path":"/var/lib/skiff/state","encrypted":true}],"stateful_dns":[{"meta":{"logical_id":"stateful-dns:ledger:0","kind":"StatefulDNSIdentity","name":"skiff-dev-ledger-dns-0","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/member-ordinal":"0","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.identity"},{"path":"$.stateful.replicas"}]},"member_ordinal":0,"hostname_prefix":"ledger","dns_name":"ledger-0"}],"stateful_recipes":[{"meta":{"logical_id":"stateful-recipe:ledger","kind":"StatefulRecipeRuntime","name":"skiff-dev-ledger-recipe-runtime","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.recipe"}]},"name":"sqlite","artifact":{"type":"oci","ref":"registry.example.com/ledger@sha256:abc123"},"command":["/bin/ledger"],"ports":{"client":9000},"health_check":{"type":"http","path":"/healthz","port":9000,"interval":"10s","timeout":"2s"},"metrics":{"enabled":false},"config":{"artifact":{"ref":"registry.example.com/ledger@sha256:abc123","type":"oci"},"runtime":{"command":["/bin/ledger"],"health":{"path":"/healthz","port":9000},"ports":{"client":9000}}}}],"snapshot_policies":[{"meta":{"logical_id":"stateful-snapshot-policy:ledger","kind":"StatefulSnapshotPolicy","name":"skiff-dev-ledger-snapshot-policy","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.recipe.config.snapshots"}]},"enabled":false}],"update_policies":[{"meta":{"logical_id":"stateful-update-policy:ledger","kind":"StatefulUpdatePolicy","name":"skiff-dev-ledger-update-policy","tags":{"skiff.dev/env":"dev","skiff.dev/graph":"service/dev/ledger","skiff.dev/managed":"true","skiff.dev/recipe":"sqlite","skiff.dev/service":"ledger","skiff.dev/stateful-group":"ledger"},"source":[{"path":"$.stateful.update"}]},"strategy":"ordered"}]}}`
	if string(body) != want {
		t.Fatalf("stateful IR golden mismatch\nwant:\n%s\n\ngot:\n%s", want, string(body))
	}
}

func TestCompileStackPackageManagedDatabase(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: payments
  env: dev
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/payments-api:latest
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.2.0"
      config:
        mode: managed
        engine: postgres
        version: "16"
        size: small
        maxReplicaLagBytes: 1048576
        synchronous: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	lockDigest := testDigest("c")
	graph, err := compiler.Compile(context.Background(), *doc, packageCompilerOptions(lockDigest, "postgres-ha", "skiff.dev/postgres-ha", internalpackages.Manifest{
		APIVersion: "skiff.dev/package/v1alpha1",
		Kind:       "Package",
		Name:       "postgres-ha",
		Version:    "1.2.0",
		Exports: internalpackages.ManifestExports{
			Dependencies:          []string{"postgres-ha"},
			OperationProfiles:     []string{"primary-switchover-update"},
			ManagedOperations:     []string{"postgres.managed.failover", "postgres.managed.backup", "postgres.managed.restore", "postgres.managed.rotate_credentials", "postgres.managed.inspect_topology"},
			SelfManagedOperations: []string{"primary-switchover-update", "postgres.backup", "postgres.restore", "postgres.rejoin_replica", "postgres.verify_replica_lag"},
			PackageSteps:          []string{"postgres.verify_replica_lag", "postgres.switchover", "postgres.verify_timeline"},
		},
	}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if graph.PackageLockDigest != lockDigest || len(graph.Packages) != 1 {
		t.Fatalf("package lock/provenance missing: digest=%q packages=%+v", graph.PackageLockDigest, graph.Packages)
	}
	if len(graph.Resources.ManagedDatabases) != 1 {
		t.Fatalf("package managed database missing: %+v", graph.Resources.ManagedDatabases)
	}
	db := graph.Resources.ManagedDatabases[0]
	if db.Engine != "postgres" || db.ConnectionSecretRef == "" || db.Meta.Tags[ir.TagPackage] != "skiff.dev/postgres-ha" {
		t.Fatalf("managed database not expanded with package metadata: %+v", db)
	}
	if db.ReplicationMode != "sync" || db.FailoverPolicy.MaxReplicaLag != "1048576B" || !db.FailoverPolicy.RequireApproval {
		t.Fatalf("managed database missing package HA policy: %+v", db)
	}
	if !hasPackageSource(db.Meta.Source, "skiff.dev/postgres-ha", lockDigest) {
		t.Fatalf("managed database missing package source: %+v", db.Meta.Source)
	}
	runtime := graph.Resources.RuntimeManifests[0]
	if runtime.Env["DATABASE_URL"] != db.ConnectionSecretRef {
		t.Fatalf("runtime DATABASE_URL = %q, want %q", runtime.Env["DATABASE_URL"], db.ConnectionSecretRef)
	}
	if len(graph.Resources.DatabaseSecrets) != 1 || len(graph.Resources.DatabaseBindings) != 1 {
		t.Fatalf("database secret/binding missing: secrets=%+v bindings=%+v", graph.Resources.DatabaseSecrets, graph.Resources.DatabaseBindings)
	}
	if len(graph.Resources.PackageOperations) != 1 || graph.Resources.PackageOperations[0].OperationProfiles[0] != "primary-switchover-update" {
		t.Fatalf("package operations missing: %+v", graph.Resources.PackageOperations)
	}
	ops := graph.Resources.PackageOperations[0]
	if ops.Mode != "managed" || len(ops.ManagedOperations) != 5 || len(ops.SelfManagedOperations) == 0 {
		t.Fatalf("package operation JSON does not distinguish managed/self-managed ops: %+v", ops)
	}
}

func TestCompileStackPostgresHASelfManagedDependency(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: payments
  env: dev
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/payments-api:latest
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: self-managed
        endpoint: secret://stateful/payments-db/connection-url
        version: "16"
        replicas: 2
        maxReplicaLagBytes: 65536
        synchronous: false
        volume:
          size: 100Gi
        artifact:
          type: oci
          ref: registry.example.com/postgres-ha@sha256:abc123
        runtime:
          command: ["/usr/local/bin/postgres-ha"]
          ports:
            postgres: 5432
            health: 8008
          health:
            path: /healthz
            port: 8008
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	lockDigest := testDigest("e")
	graph, err := compiler.Compile(context.Background(), *doc, packageCompilerOptions(lockDigest, "postgres-ha", "skiff.dev/postgres-ha", internalpackages.Manifest{
		APIVersion: "skiff.dev/package/v1alpha1",
		Kind:       "Package",
		Name:       "postgres-ha",
		Version:    "1.0.0",
		Exports: internalpackages.ManifestExports{
			Dependencies:          []string{"postgres-ha"},
			OperationProfiles:     []string{"primary-switchover-update"},
			ManagedOperations:     []string{"postgres.managed.failover", "postgres.managed.backup", "postgres.managed.restore", "postgres.managed.rotate_credentials", "postgres.managed.inspect_topology"},
			SelfManagedOperations: []string{"primary-switchover-update", "postgres.backup", "postgres.restore", "postgres.rejoin_replica", "postgres.verify_replica_lag"},
			PackageSteps:          []string{"postgres.verify_replica_lag", "postgres.switchover", "postgres.verify_timeline"},
		},
	}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(graph.Resources.ManagedDatabases) != 0 || len(graph.Resources.StatefulGroups) != 1 {
		t.Fatalf("self-managed dependency should compile to StatefulGroup only: managed=%+v stateful=%+v", graph.Resources.ManagedDatabases, graph.Resources.StatefulGroups)
	}
	group := graph.Resources.StatefulGroups[0]
	if group.Replicas != 2 || group.Meta.Tags[ir.TagPackage] != "skiff.dev/postgres-ha" {
		t.Fatalf("unexpected self-managed group: %+v", group)
	}
	if graph.Resources.RuntimeManifests[0].Env["DATABASE_URL"] != "secret://stateful/payments-db/connection-url" {
		t.Fatalf("DATABASE_URL binding = %q", graph.Resources.RuntimeManifests[0].Env["DATABASE_URL"])
	}
	if len(graph.Resources.PackageOperations) != 1 {
		t.Fatalf("package operation missing: %+v", graph.Resources.PackageOperations)
	}
	ops := graph.Resources.PackageOperations[0]
	if ops.Mode != "self-managed" || len(ops.SelfManagedOperations) != 5 || !containsString(ops.PackageSteps, "postgres.switchover") {
		t.Fatalf("self-managed package operations missing: %+v", ops)
	}
	if !strings.Contains(string(ops.Config), `"maxReplicaLagBytes":65536`) {
		t.Fatalf("package operation config did not preserve lag gate: %s", string(ops.Config))
	}
}

func TestCompileStackPackageStatefulDependency(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: dev
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/orders-api:latest
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: stream
      uses: skiff.dev/jetstream
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 3
        volume:
          size: 50Gi
        artifact:
          type: oci
          ref: registry.example.com/nats@sha256:abc123
        runtime:
          command: ["/nats-server", "-js"]
          ports:
            client: 4222
            monitoring: 8222
          health:
            path: /healthz
            port: 8222
  bindings:
    - from: api
      to: stream
      as: NATS_URL
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	lockDigest := testDigest("d")
	graph, err := compiler.Compile(context.Background(), *doc, packageCompilerOptions(lockDigest, "jetstream", "skiff.dev/jetstream", internalpackages.Manifest{
		APIVersion: "skiff.dev/package/v1alpha1",
		Kind:       "Package",
		Name:       "jetstream",
		Version:    "1.0.0",
		Exports: internalpackages.ManifestExports{
			Dependencies:      []string{"jetstream"},
			OperationProfiles: []string{"jetstream-rolling-update"},
			PackageSteps:      []string{"nats.verify_cluster"},
		},
	}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(graph.Resources.StatefulGroups) != 1 || graph.Resources.StatefulGroups[0].Replicas != 3 {
		t.Fatalf("stateful dependency not expanded: %+v", graph.Resources.StatefulGroups)
	}
	if len(graph.Resources.StatefulMembers) != 3 || len(graph.Resources.StatefulVolumes) != 3 {
		t.Fatalf("stateful members/volumes missing: members=%+v volumes=%+v", graph.Resources.StatefulMembers, graph.Resources.StatefulVolumes)
	}
	if graph.Resources.RuntimeManifests[0].Env["NATS_URL"] != "skiff://stateful/orders-stream" {
		t.Fatalf("NATS_URL binding = %q", graph.Resources.RuntimeManifests[0].Env["NATS_URL"])
	}
	group := graph.Resources.StatefulGroups[0]
	if group.Meta.Tags[ir.TagService] != "orders-api" || group.Meta.Tags[ir.TagDependency] != "stream" {
		t.Fatalf("stateful package tags missing root service/dependency: %+v", group.Meta.Tags)
	}
	if !hasPackageSource(group.Meta.Source, "skiff.dev/jetstream", lockDigest) {
		t.Fatalf("stateful group missing package source: %+v", group.Meta.Source)
	}
	if len(graph.Resources.PackageOperations) != 1 || graph.Resources.PackageOperations[0].Package.Ref != "skiff.dev/jetstream" {
		t.Fatalf("stateful package operations missing: %+v", graph.Resources.PackageOperations)
	}
}

func TestSemanticDiffIgnoresResourceOrdering(t *testing.T) {
	graph := *compileExample(t)
	extra := graph.Resources.LogConfigs[0]
	extra.Meta.LogicalID = "logs:payments-api-extra"
	extra.Meta.Name = "skiff-prod-payments-api-extra-logs"

	left := graph
	left.Resources.LogConfigs = []ir.LogConfig{graph.Resources.LogConfigs[0], extra}
	right := graph
	right.Resources.LogConfigs = []ir.LogConfig{extra, graph.Resources.LogConfigs[0]}

	diff, err := ir.SemanticDiff(left, right)
	if err != nil {
		t.Fatalf("semantic diff: %v", err)
	}
	if diff.Changed {
		t.Fatalf("reorder-only graph diff reported changes: %+v", diff)
	}
}

func TestCompileMultiRegionStack(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: MultiRegionStack
metadata:
  name: orders
  env: prod
multiRegion:
  primaryRegion: us-west-2
  secondaryRegions:
    - us-east-1
  service:
    name: api
    artifact:
      type: oci
      ref: registry.example.com/orders-api@sha256:abc123
    runtime:
      port: 8080
      health:
        path: /healthz
  database:
    name: db
    engine: postgres
    version: "16"
    size: small
  trafficPolicy:
    mode: weighted-dns
    host: orders.example.com
    weights:
      - region: us-west-2
        weight: 100
      - region: us-east-1
        weight: 0
  databaseReplication:
    mode: async
    maxReplicaLag: 30s
  failoverPolicy:
    freezeWrites: true
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if graph.Service != "orders" || len(graph.Resources.GlobalTraffic) != 1 {
		t.Fatalf("graph target/global traffic mismatch: %+v", graph)
	}
	if len(graph.Resources.ManagedDatabases) != 2 {
		t.Fatalf("managed databases = %+v, want primary and replica", graph.Resources.ManagedDatabases)
	}
	roles := map[string]string{}
	for _, db := range graph.Resources.ManagedDatabases {
		roles[db.Region] = db.Role
		if db.Meta.Tags[ir.TagRegion] != db.Region {
			t.Fatalf("database %s missing region tag: %+v", db.Meta.LogicalID, db.Meta.Tags)
		}
	}
	if roles["us-west-2"] != "primary" || roles["us-east-1"] != "replica" {
		t.Fatalf("database roles = %+v", roles)
	}
	traffic := graph.Resources.GlobalTraffic[0]
	if traffic.PrimaryRegion != "us-west-2" || len(traffic.Regions) != 2 || traffic.Regions[1].Weight != 0 {
		t.Fatalf("traffic policy = %+v", traffic)
	}
}

func compileExample(t *testing.T) *ir.Graph {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load example spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile example spec: %v", err)
	}
	return graph
}

func resourceMetas(graph *ir.Graph) []ir.ResourceMeta {
	var out []ir.ResourceMeta
	for _, resource := range graph.Resources.WorkloadIdentities {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.IAMRoles {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.SecurityGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.LogConfigs {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.MetricConfigs {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.TargetGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.Listeners {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.ManagedDatabases {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.DatabaseSecrets {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.DatabaseBindings {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.ObjectStores {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.ObjectStoreBindings {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.InstanceTemplates {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.AutoscalingGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.RuntimeManifests {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.StatefulGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.StatefulMembers {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.StatefulVolumes {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.StatefulDNS {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.StatefulRecipes {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.SnapshotPolicies {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.UpdatePolicies {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.PackageOperations {
		out = append(out, resource.Meta)
	}
	return out
}

func packageCompilerOptions(lockDigest, name, ref string, manifest internalpackages.Manifest) compiler.Options {
	entry := internalpackages.LockEntry{
		Name:           name,
		Ref:            ref,
		Version:        manifest.Version,
		Digest:         testDigest("a"),
		SignatureRef:   "oci://registry.example.com/skiff/" + name + ".sig@" + testDigest("b"),
		Source:         "oci://registry.example.com/skiff/" + name + ":" + manifest.Version,
		ManifestDigest: testDigest("b"),
		ResolvedAt:     "2026-05-20T02:43:35Z",
	}
	return compiler.Options{
		PackageLock: &internalpackages.LockFile{
			Schema:   "skiff.lock/v1alpha1",
			Packages: []internalpackages.LockEntry{entry},
		},
		PackageLockDigest: lockDigest,
		PackageManifests: map[string]internalpackages.Manifest{
			name:         manifest,
			ref:          manifest,
			entry.Source: manifest,
		},
	}
}

func testDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64)
}

func hasPackageSource(source []ir.SourceRef, pkg, lockDigest string) bool {
	for _, ref := range source {
		if ref.Package == pkg && ref.LockfileDigest == lockDigest {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
