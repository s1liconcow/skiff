package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/provider/applecontainer"
)

func TestAppleAPIPostgresHAStackDemo(t *testing.T) {
	resetSkiffEnv(t)
	if os.Getenv("SKIFF_APPLE_API_POSTGRES_HA_E2E") != "1" {
		t.Skip("set SKIFF_APPLE_API_POSTGRES_HA_E2E=1 to run the Apple API plus postgres-ha stack demo")
	}
	containerPath, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ordersImage := envDefault("SKIFF_APPLE_ORDERS_API_IMAGE", "localhost/orders-rpc:apple")
	postgresImage := envDefault("SKIFF_APPLE_POSTGRES_HA_IMAGE", "localhost/postgres-ha:apple")
	env := "local"
	stack := "ordersdemo" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	service := stack + "-api"
	group := stack + "-db"
	traceID := "tr_apple_api_postgres_ha"

	cli := appleContainerCLI{path: containerPath}
	persist := appleContainerPersistEnabled()
	runID := fmt.Sprintf("skiff-api-postgres-ha-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfs := startRustFSContainer(t, ctx, cli, runID, freePort(t), persist)
	configureRustFSEnv(t, rustfs)
	_ = rustfsObjectStore(t, ctx, rustfs)
	stateURI := "s3://" + rustfs.bucket

	servicePortBase := reserveAppleStatefulPortBase(t, 1, 2)
	statefulPortBase := reserveDisjointAppleStatefulPortBase(t, servicePortBase)

	report := newE2EReport(t, "apple-api-postgres-ha", service, env, traceID)
	report.StateURI = stateURI
	report.APIURL = "http://127.0.0.1:" + strconv.Itoa(servicePortBase)
	defer writeE2EReport(t, report)
	if err := os.MkdirAll(report.reportDir, 0o755); err != nil {
		t.Fatalf("create e2e report dir: %v", err)
	}

	t.Setenv("SKIFF_APPLE_SERVICE_PORT_BASE", strconv.Itoa(servicePortBase))
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", strconv.Itoa(statefulPortBase))
	if !persist {
		t.Cleanup(func() {
			cleanupAppleService(context.Background(), cli, env, service, 2)
			cleanupAppleStatefulGroup(context.Background(), cli, env, group, 3, 3)
		})
		report.CleanupStatus = "Apple API, postgres-ha member containers, volumes, and RustFS state registered with test cleanup"
	} else {
		report.CleanupStatus = "Apple API, postgres-ha member containers, volumes, and RustFS state left running for inspection"
	}

	lockfile := filepath.Join(report.reportDir, "skiff.lock.json")
	cacheRoot := filepath.Join(report.reportDir, "package-cache")
	specPath := writeAppleAPIPostgresHASpec(t, report.reportDir, stack, env, group, ordersImage, postgresImage)
	report.fact("apple_api_postgres_ha_images", "orders="+ordersImage+" postgres-ha="+postgresImage)
	report.fact("apple_api_postgres_ha_state", "RustFS state="+stateURI+" endpoint="+rustfs.endpoint)

	runSkiffCLI(t, report,
		"pkg", "add", "skiff.dev/postgres-ha",
		"--registry-dir", filepath.Join(repoRootForTest(t), "packages"),
		"--lockfile", lockfile,
		"--cache", cacheRoot,
		"--format", "json",
		"--trace-id", traceID,
	)
	runSkiffCLI(t, report, "validate", specPath, "--format", "json", "--trace-id", traceID)
	runSkiffCLI(t, report,
		"deploy", specPath,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--lockfile", lockfile,
		"--cache", cacheRoot,
		"--yes",
		"--format", "json",
		"--trace-id", traceID,
	)

	waitForOrdersReady(t, ctx, report.APIURL)
	first := postOrdersRPC(t, ctx, report.APIURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "add-apple-1",
		"method":  "orders.add",
		"params": map[string]any{
			"customer": "acme",
			"sku":      "sku-123",
			"quantity": 2,
		},
	})
	second := postOrdersRPC(t, ctx, fmt.Sprintf("http://127.0.0.1:%d", servicePortBase+1), map[string]any{
		"jsonrpc": "2.0",
		"id":      "add-apple-2",
		"method":  "orders.add",
		"params": map[string]any{
			"customer": "beta",
			"sku":      "sku-456",
			"quantity": 3,
		},
	})
	list := postOrdersRPC(t, ctx, report.APIURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "list-apple",
		"method":  "orders.list",
		"params": map[string]any{
			"limit": 20,
		},
	})
	if !strings.Contains(string(list), `"customer":"acme"`) || !strings.Contains(string(list), `"customer":"beta"`) {
		t.Fatalf("orders.list did not return both inserted rows:\n%s", string(list))
	}
	metrics := getText(t, ctx, report.APIURL+"/metrics")
	if !strings.Contains(metrics, "orders_rpc_orders_total 2") {
		t.Fatalf("metrics = %q, want database-backed count 2", metrics)
	}

	report.fact("apple_api_postgres_ha_ready", report.APIURL+"/readyz returned ok")
	report.fact("apple_api_postgres_ha_write", "first="+string(first)+" second="+string(second))
	report.fact("apple_api_postgres_ha_read", string(list))
	report.fact("apple_api_postgres_ha_metrics", strings.TrimSpace(metrics))
	runSkiffCLI(t, report,
		"ops", "list", group,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--lockfile", lockfile,
		"--cache", cacheRoot,
		"--format", "json",
		"--trace-id", traceID,
	)
	report.fact("apple_api_postgres_ha_package_operation", "postgres-ha operations are listed for "+group)
}

func writeAppleAPIPostgresHASpec(t *testing.T, dir, stack, env, group, ordersImage, postgresImage string) string {
	t.Helper()
	path := filepath.Join(dir, "orders-api-postgres-ha.skiff.yaml")
	body := fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: %s
  env: %s
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: %s
      runtime:
        port: 8080
        env:
          SKIFF_STACK: %s
          PORT: "8080"
        health:
          path: /readyz
      scale:
        min: 2
        max: 2
      network:
        ingress:
          type: internal-http
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: self-managed
        endpoint: secret://stateful/%s/connection-url
        replicas: 3
        maxReplicaLagBytes: 65536
        volume:
          size: 1Gi
          mountPath: /data
          encrypted: true
        artifact:
          type: oci
          ref: %s
        runtime:
          command:
            - /usr/local/bin/postgres-ha
          ports:
            postgres: 5432
            admin: 8008
          health:
            path: /healthz
            port: 8008
        update:
          strategy: ordered
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`, strconv.Quote(stack), strconv.Quote(env), strconv.Quote(ordersImage), stack, group, strconv.Quote(postgresImage))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForOrdersReady(t *testing.T, ctx context.Context, apiURL string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		body, err := getTextOK(ctx, apiURL+"/readyz")
		if err == nil && strings.TrimSpace(body) == "ok" {
			return
		}
		if err != nil {
			last = err.Error()
		} else {
			last = body
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for API readiness: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("API did not become ready: %s", last)
}

func postOrdersRPC(t *testing.T, ctx context.Context, apiURL string, payload map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST %s/rpc returned %d: %s", apiURL, resp.StatusCode, string(out))
	}
	if strings.Contains(string(out), `"error"`) {
		t.Fatalf("JSON-RPC error from %s/rpc: %s", apiURL, string(out))
	}
	return out
}

func getText(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	body, err := getTextOK(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func getTextOK(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(body), fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return string(body), nil
}

func reserveDisjointAppleStatefulPortBase(t *testing.T, servicePortBase int) int {
	t.Helper()
	start := 26000 + int(time.Now().UnixNano()%1000)
	for base := start; base < 60000-3*100; base += 23 {
		if appleAPIPortWindowsOverlap(servicePortBase, base) {
			continue
		}
		if applePortsAvailable(base, 3, 2) {
			return base
		}
	}
	t.Fatal("could not reserve disjoint API and StatefulGroup port windows")
	return 0
}

func appleAPIPortWindowsOverlap(servicePortBase, statefulPortBase int) bool {
	servicePorts := map[int]bool{
		servicePortBase:     true,
		servicePortBase + 1: true,
	}
	for member := 0; member < 3; member++ {
		for offset := 0; offset < 2; offset++ {
			if servicePorts[statefulPortBase+member*100+offset] {
				return true
			}
		}
	}
	return false
}

func applePortsAvailable(base, members, portsPerMember int) bool {
	var listeners []net.Listener
	ok := true
	for member := 0; member < members && ok; member++ {
		for offset := 0; offset < portsPerMember; offset++ {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+member*100+offset))
			if err != nil {
				ok = false
				break
			}
			listeners = append(listeners, listener)
		}
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return ok
}

func cleanupAppleService(ctx context.Context, cli appleContainerCLI, env, service string, replicas int) {
	for replica := 0; replica < replicas; replica++ {
		name := fmt.Sprintf("skiff-%s-%s-r%d-g1", appleE2EPathSafe(env), appleE2EPathSafe(service), replica)
		_, _ = cli.run(ctx, "stop", "--time", "2", name)
		_, _ = cli.run(ctx, "delete", "--force", name)
	}
}
