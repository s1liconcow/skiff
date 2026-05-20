package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestOpsemAppleStatefulHarness(t *testing.T) {
	resetSkiffEnv(t)
	if os.Getenv("SKIFF_OPSEM_E2E") != "1" {
		t.Skip("set SKIFF_OPSEM_E2E=1 and SKIFF_E2E_OPSEM_IMAGE to run the live opsem Apple e2e")
	}
	imageName := strings.TrimSpace(os.Getenv("SKIFF_E2E_OPSEM_IMAGE"))
	if imageName == "" {
		t.Skip("SKIFF_E2E_OPSEM_IMAGE must point to a skiff-opsem OCI image built from tests/fixtures/opsem/Dockerfile")
	}
	containerPath, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	image := pinnedImageForE2E(t, ctx, "SKIFF_E2E_OPSEM_IMAGE", imageName)
	cli := appleContainerCLI{path: containerPath}
	persist := appleContainerPersistEnabled()
	runID := fmt.Sprintf("skiff-opsem-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfs := startRustFSContainer(t, ctx, cli, runID, freePort(t), persist)
	configureRustFSEnv(t, rustfs)
	_ = rustfsObjectStore(t, ctx, rustfs)
	stateURI := "s3://" + rustfs.bucket
	service := "opsem-stateful"
	env := "prod"
	traceID := "tr_opsem_e2e"
	report := newE2EReport(t, "opsem-apple-stateful", service, env, traceID)
	defer writeE2EReport(t, report)

	portBase := reserveAppleStatefulPortBase(t, 3, 1)
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", strconv.Itoa(portBase))
	specPath := writeOpsemStatefulSpec(t, report.reportDir, service, env, image)
	contexts := writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{})
	useAppleContext(t, contexts, appleDirectContext)
	if persist {
		report.CleanupStatus = "opsem Apple containers and RustFS state left running for inspection"
	} else {
		report.CleanupStatus = "opsem Apple containers, volumes, and RustFS state registered with test cleanup"
		t.Cleanup(func() { cleanupAppleStatefulGroup(context.Background(), cli, env, service, 3, 3) })
	}

	var applied appleStatefulApplyOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "apply", specPath,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--operation-id", "op_opsem_apply",
		"--format", "json",
		"--trace-id", traceID,
	), &applied)
	if !applied.OK || len(applied.Result.MemberControls) != 3 {
		t.Fatalf("unexpected opsem apply output: %+v", applied)
	}
	report.addOperationID("op_opsem_apply")
	for _, resource := range applied.Result.ProviderResources {
		report.addProviderID(resource.ProviderID)
	}

	member1 := opsemState(t, ctx, portBase+100)
	if member1.Mode != "partition-isr" || member1.Member != 1 || len(member1.Partitions) != 1 || len(member1.Partitions[0].ISR) != 3 {
		t.Fatalf("unexpected initial opsem state: %+v", member1)
	}
	opsemPost(t, ctx, portBase+100, "/admin/fail", map[string]string{"type": "isr-below-min"})
	member1 = opsemState(t, ctx, portBase+100)
	if len(member1.Partitions[0].ISR) >= member1.Partitions[0].MinISR {
		t.Fatalf("opsem did not inject ISR failure: %+v", member1)
	}
	report.fact("opsem_failure_injection", "injected deterministic ISR-below-min failure into member 1")

	var replaced appleStatefulReplacementOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "replace-member", service,
		"--member", "1",
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--operation-id", "op_opsem_replace",
		"--saga-id", "saga_opsem_replace",
		"--yes",
		"--format", "json",
		"--trace-id", traceID,
	), &replaced)
	if !replaced.OK || !replaced.Result.MovesVolume || replaced.Result.NewInstanceID == replaced.Result.OldInstanceID {
		t.Fatalf("unexpected opsem replacement output: %+v", replaced)
	}
	report.addOperationID("op_opsem_replace")
	report.addSagaID("saga_opsem_replace")
	report.addProviderID(replaced.Result.NewInstanceID)
	member1 = opsemState(t, ctx, portBase+100)
	if member1.Generation != 2 || len(member1.Partitions[0].ISR) >= member1.Partitions[0].MinISR {
		t.Fatalf("opsem replacement did not preserve durable unsafe state with new generation: %+v", member1)
	}
	opsemPost(t, ctx, portBase+100, "/admin/recover", nil)
	member1 = opsemState(t, ctx, portBase+100)
	if len(member1.Partitions[0].ISR) != 3 || len(member1.Failures) != 0 {
		t.Fatalf("opsem recover did not restore safe state: %+v", member1)
	}
	report.fact("opsem_volume_movement", "replace-member preserved opsem state across Apple volume movement and recovered through admin API")

}

func writeOpsemStatefulSpec(t *testing.T, dir, service, env, image string) string {
	t.Helper()
	return writeOpsemStatefulSpecMode(t, dir, service, env, image, "partition-isr")
}

func writeOpsemStatefulSpecMode(t *testing.T, dir, service, env, image, mode string) string {
	t.Helper()
	path := filepath.Join(dir, service+".skiff.yaml")
	body := fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: %s
  env: %s
stateful:
  replicas: 3
  members:
    - ordinal: 0
      dnsName: %s
    - ordinal: 1
      dnsName: %s
    - ordinal: 2
      dnsName: %s
  volume:
    size: 256Mi
    mountPath: /data
    encrypted: true
  recipe:
    name: skiff-opsem
    config:
      artifact:
        type: oci
        ref: %s
      runtime:
        command:
          - /skiff-opsem
          - --addr
          - :8080
          - --state-dir
          - /data
          - --mode
          - %s
        ports:
          admin: 8080
        health:
          path: /healthz
          port: 8080
  update:
    strategy: ordered
`, strconv.Quote(service), strconv.Quote(env), strconv.Quote(service+"-0.local"), strconv.Quote(service+"-1.local"), strconv.Quote(service+"-2.local"), strconv.Quote(image), strconv.Quote(mode))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type opsemStateBody struct {
	Mode       string `json:"mode"`
	Member     int    `json:"member"`
	Members    int    `json:"members"`
	Generation int64  `json:"generation"`
	Role       string `json:"role"`
	Term       int64  `json:"term"`
	Leader     int    `json:"leader"`
	Lag        int64  `json:"lag"`
	Quorum     struct {
		Required  int  `json:"required"`
		Available int  `json:"available"`
		Healthy   bool `json:"healthy"`
	} `json:"quorum"`
	Failures   map[string]string
	Partitions []struct {
		ISR    []int `json:"isr"`
		MinISR int   `json:"min_isr"`
	} `json:"partitions"`
	Slots struct {
		Missing    []int `json:"missing"`
		CoverageOK bool  `json:"coverage_ok"`
	} `json:"slots"`
	Shards []struct {
		Name   string `json:"name"`
		Health string `json:"health"`
	} `json:"shards"`
	Relocation struct {
		Active bool   `json:"active"`
		Status string `json:"status"`
	} `json:"relocation"`
	Draining bool `json:"draining"`
}

func opsemState(t *testing.T, ctx context.Context, port int) opsemStateBody {
	t.Helper()
	body := opsemRequest(t, ctx, http.MethodGet, port, "/admin/state", nil, http.StatusOK)
	var out opsemStateBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode opsem state: %v\n%s", err, string(body))
	}
	return out
}

func opsemPost(t *testing.T, ctx context.Context, port int, path string, body any) {
	t.Helper()
	opsemRequest(t, ctx, http.MethodPost, port, path, body, http.StatusOK)
}

func opsemRequest(t *testing.T, ctx context.Context, method string, port int, path string, body any, want int) []byte {
	t.Helper()
	var rawBody []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rawBody = raw
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		var reader io.Reader
		if rawBody != nil {
			reader = bytes.NewReader(rawBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), reader)
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			req.Header.Set("content-type", "application/json")
		}
		resp, err := client.Do(req)
		if err == nil {
			raw, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == want {
				return raw
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("status %d body %q", resp.StatusCode, string(raw))
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("opsem request failed: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("opsem %s %s on port %d failed: %v", method, path, port, lastErr)
	return nil
}
