package e2e_test

import (
	"context"
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
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestAppleStatefulGroupRustFSE2E(t *testing.T) {
	resetSkiffEnv(t)
	if os.Getenv("SKIFF_APPLE_STATEFUL_E2E") != "1" {
		t.Skip("set SKIFF_APPLE_STATEFUL_E2E=1 to run the Apple StatefulGroup/RustFS e2e")
	}
	containerPath, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	image := pinnedImageForE2E(t, ctx, "SKIFF_E2E_STATEFUL_IMAGE", "docker.io/library/busybox:1.36")
	cli := appleContainerCLI{path: containerPath}
	persist := appleContainerPersistEnabled()
	runID := fmt.Sprintf("skiff-stateful-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfsPort := freePort(t)
	rustfs := startRustFSContainer(t, ctx, cli, runID, rustfsPort, persist)
	configureRustFSEnv(t, rustfs)

	store := rustfsObjectStore(t, ctx, rustfs)
	stateURI := "s3://" + rustfs.bucket
	service := "apple-stateful"
	env := "prod"
	traceID := "tr_apple_stateful_e2e"
	applyOperationID := "op_apple_stateful_apply"
	updateOperationID := "op_apple_stateful_update"
	replaceOperationID := "op_apple_stateful_replace"
	replaceSagaID := "saga_apple_stateful_replace"
	report := newE2EReport(t, "apple-stateful", service, env, traceID)
	defer writeE2EReport(t, report)

	portBase := reserveAppleStatefulPortBase(t, 3, 2)
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", strconv.Itoa(portBase))
	specPath := writeAppleStatefulSpec(t, report.reportDir, service, env, image)
	contexts := writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{})
	useAppleContext(t, contexts, appleDirectContext)

	if persist {
		report.CleanupStatus = "Apple StatefulGroup containers and RustFS state left running for inspection"
	} else {
		report.CleanupStatus = "Apple StatefulGroup containers, volumes, and RustFS state registered with test cleanup"
		t.Cleanup(func() { cleanupAppleStatefulGroup(context.Background(), cli, env, service, 3, 3) })
	}
	report.fact("apple_stateful_ports", fmt.Sprintf("reserved Apple StatefulGroup host port base %d", portBase))
	report.RecommendedNextCommands = append(report.RecommendedNextCommands,
		"source "+shellQuote(contexts.envPath),
		"SKIFF_CONTEXT="+appleDirectContext+" skiff stateful inspect "+service+" --format json --trace-id "+traceID,
		"SKIFF_CONTEXT="+appleDirectContext+" skiff stateful replace-member "+service+" --member 1 --yes --format json --trace-id "+traceID,
	)

	var applied appleStatefulApplyOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "apply", specPath,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--operation-id", applyOperationID,
		"--format", "json",
		"--trace-id", traceID,
	), &applied)
	if !applied.OK || applied.Result.Group != service || len(applied.Result.MemberControls) != 3 {
		t.Fatalf("unexpected stateful apply output: %+v", applied)
	}
	report.addOperationID(applyOperationID)
	for _, resource := range applied.Result.ProviderResources {
		report.addProviderID(resource.ProviderID)
	}
	for _, key := range applied.Result.MutableObjectWrites {
		report.addObjectPath(key)
	}
	for _, key := range applied.Result.ImmutableWrites {
		report.addObjectPath(key)
	}

	initialBodies := make(map[int]string)
	for member := 0; member < 3; member++ {
		body := waitAppleStatefulHTTP(t, ctx, portBase+member*100+1)
		want := fmt.Sprintf("member-%d-generation-1", member)
		if !strings.Contains(body, want) {
			t.Fatalf("member %d body = %q, want %q", member, body, want)
		}
		initialBodies[member] = body
	}
	report.fact("apple_stateful_apply", "deployed three Apple containers with one durable Apple volume per StatefulGroup member")

	var updated appleStatefulOrderedOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"deploy", specPath,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--release-id", "rel_apple_stateful_update",
		"--operation-id", updateOperationID,
		"--yes",
		"--format", "json",
		"--trace-id", traceID,
	), &updated)
	if !updated.OK || updated.Result.OperationID != updateOperationID || !updated.Result.InPlace || updated.Result.ReplacesVM {
		t.Fatalf("unexpected ordered update output: %+v", updated)
	}
	report.addOperationID(updateOperationID)
	report.addSagaID(updated.Result.SagaID)
	for member := 0; member < 3; member++ {
		if body := waitAppleStatefulHTTP(t, ctx, portBase+member*100+1); body != initialBodies[member] {
			t.Fatalf("member %d in-place update changed durable body from %q to %q", member, initialBodies[member], body)
		}
	}
	report.fact("apple_stateful_ordered_update", "ran ordered in-place update against live Apple member containers")

	var replaced appleStatefulReplacementOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "replace-member", service,
		"--member", "1",
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--operation-id", replaceOperationID,
		"--saga-id", replaceSagaID,
		"--yes",
		"--format", "json",
		"--trace-id", traceID,
	), &replaced)
	if !replaced.OK || replaced.Result.OperationID != replaceOperationID || replaced.Result.Member != 1 || !replaced.Result.MovesVolume || replaced.Result.NewInstanceID == replaced.Result.OldInstanceID {
		t.Fatalf("unexpected replacement output: %+v", replaced)
	}
	report.addOperationID(replaceOperationID)
	report.addSagaID(replaceSagaID)
	report.addProviderID(replaced.Result.OldInstanceID)
	report.addProviderID(replaced.Result.NewInstanceID)
	if body := waitAppleStatefulHTTP(t, ctx, portBase+100+1); body != initialBodies[1] {
		t.Fatalf("replacement did not preserve member 1 volume data: before=%q after=%q", initialBodies[1], body)
	}
	report.fact("apple_stateful_replace_member", "replaced member 1 by moving its persistent Apple volume to a new member container")

	var inspected appleStatefulInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "inspect", service,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--format", "json",
		"--trace-id", traceID,
	), &inspected)
	if !inspected.OK || len(inspected.Result.MemberControls) != 3 || inspected.Result.MemberControls[1].Generation != 2 {
		t.Fatalf("unexpected direct stateful inspect output: %+v", inspected)
	}

	localSkiffd := startLocalAppleSkiffd(t, ctx, store, report, rustfs, stateURI, env, traceID, replaced.Result.NewInstanceID, persist, nil)
	contexts = writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{APIURL: localSkiffd.url, SkiffdPID: report.SkiffdPID, SkiffdLogPath: report.SkiffdLogPath})
	useAppleContext(t, contexts, appleAPIContext)
	var apiStatus appleStatefulStatusOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"stateful", "status", service,
		"--fresh",
		"--format", "json",
		"--trace-id", traceID,
	), &apiStatus)
	if !apiStatus.OK || apiStatus.Result.Group != service || apiStatus.Result.Health == "" || len(apiStatus.Result.Members) != 3 {
		t.Fatalf("unexpected local skiffd stateful status: %+v", apiStatus)
	}
	report.fact("apple_stateful_skiffd", "validated local skiffd API status for the RustFS-backed StatefulGroup")
}

func writeAppleStatefulSpec(t *testing.T, dir, service, env, image string) string {
	t.Helper()
	path := filepath.Join(dir, "apple-stateful.skiff.yaml")
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
    name: apple-busybox-stateful
    config:
      artifact:
        type: oci
        ref: %s
      runtime:
        command:
          - sh
          - -c
          - "mkdir -p /data; if [ ! -f /data/member.txt ]; then echo member-${SKIFF_STATEFUL_MEMBER}-generation-${SKIFF_STATEFUL_GENERATION} > /data/member.txt; fi; while true; do body=$(cat /data/member.txt); printf 'HTTP/1.1 200 OK\r\nContent-Length: %%s\r\n\r\n%%s' ${#body} \"$body\" | nc -l -p 8080; done"
        ports:
          admin: 8081
          health: 8080
        health:
          path: /healthz
          port: 8080
  update:
    strategy: ordered
`, strconv.Quote(service), strconv.Quote(env), strconv.Quote(service+"-0.local"), strconv.Quote(service+"-1.local"), strconv.Quote(service+"-2.local"), strconv.Quote(image))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func reserveAppleStatefulPortBase(t *testing.T, members, portsPerMember int) int {
	t.Helper()
	start := 24000 + int(time.Now().UnixNano()%1000)
	for base := start; base < 60000-members*100; base += 17 {
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
		if ok {
			return base
		}
	}
	t.Fatal("could not reserve Apple StatefulGroup port window")
	return 0
}

func waitAppleStatefulHTTP(t *testing.T, ctx context.Context, port int) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return strings.TrimSpace(string(body))
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("status %d body %q", resp.StatusCode, string(body))
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Apple StatefulGroup HTTP: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("Apple StatefulGroup member on port %d did not become healthy: %v", port, lastErr)
	return ""
}

func cleanupAppleStatefulGroup(ctx context.Context, cli appleContainerCLI, env, service string, members, maxGeneration int) {
	for member := 0; member < members; member++ {
		for generation := 1; generation <= maxGeneration; generation++ {
			name := fmt.Sprintf("skiff-%s-%s-m%d-g%d", appleE2EPathSafe(env), appleE2EPathSafe(service), member, generation)
			_, _ = cli.run(ctx, "stop", "--time", "2", name)
			_, _ = cli.run(ctx, "delete", "--force", name)
		}
		volume := fmt.Sprintf("skiff-%s-%s-m%d-data", appleE2EPathSafe(env), appleE2EPathSafe(service), member)
		_, _ = cli.run(ctx, "volume", "delete", volume)
	}
}

func appleE2EPathSafe(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "resource"
	}
	return out
}

type appleStatefulApplyOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Group               string                          `json:"group"`
		MemberControls      []schema.StatefulMemberControl  `json:"member_controls"`
		ProviderResources   []appleStatefulProviderResource `json:"provider_resources"`
		MutableObjectWrites []string                        `json:"mutable_object_writes"`
		ImmutableWrites     []string                        `json:"immutable_writes"`
	} `json:"result"`
}

type appleStatefulProviderResource struct {
	ProviderID string `json:"provider_id"`
}

type appleStatefulOrderedOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OperationID string `json:"operation_id"`
		SagaID      string `json:"saga_id"`
		InPlace     bool   `json:"in_place"`
		ReplacesVM  bool   `json:"replaces_vm"`
	} `json:"result"`
}

type appleStatefulReplacementOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OperationID   string `json:"operation_id"`
		Member        int    `json:"member"`
		OldInstanceID string `json:"old_instance_id"`
		NewInstanceID string `json:"new_instance_id"`
		MovesVolume   bool   `json:"moves_volume"`
	} `json:"result"`
}

type appleStatefulInspectOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		MemberControls []schema.StatefulMemberControl `json:"member_controls"`
	} `json:"result"`
}

type appleStatefulStatusOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Group   string `json:"group"`
		Health  string `json:"health"`
		Members []struct {
			Member     int    `json:"member"`
			Generation int64  `json:"generation"`
			InstanceID string `json:"instance_id"`
			VolumeID   string `json:"volume_id"`
			Phase      string `json:"phase"`
		} `json:"members"`
	} `json:"result"`
}
