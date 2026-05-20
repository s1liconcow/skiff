package spec

import (
	"encoding/json"
	"testing"
)

func TestYAMLSequenceQuotedStringWithColonSpaceStaysScalar(t *testing.T) {
	value, err := parseYAMLSubset([]byte(`
command:
  - sh
  - -c
  - "printf 'HTTP/1.1 200 OK\r\nContent-Length: %s\r\n\r\n%s' 2 ok"
`))
	if err != nil {
		t.Fatalf("parseYAMLSubset: %v", err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("root = %T, want map", value)
	}
	command, ok := root["command"].([]any)
	if !ok || len(command) != 3 {
		t.Fatalf("command = %#v, want three-item sequence", root["command"])
	}
	if _, ok := command[2].(string); !ok {
		t.Fatalf("quoted command item decoded as %T, want string: %#v", command[2], command[2])
	}
}

func TestDecodeStatefulRecipeCommandWithHTTPHeaderColon(t *testing.T) {
	doc, err := Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: apple-stateful
  env: prod
stateful:
  replicas: 1
  volume:
    mountPath: /data
  recipe:
    name: apple-busybox-stateful
    config:
      artifact:
        type: oci
        ref: docker.io/library/busybox@sha256:test
      runtime:
        command:
          - sh
          - -c
          - "while true; do body=ok; printf 'HTTP/1.1 200 OK\r\nContent-Length: %s\r\n\r\n%s' ${#body} \"$body\" | nc -l -p 8080; done"
        ports:
          health: 8080
        health:
          path: /healthz
          port: 8080
  update:
    strategy: ordered
`), DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	var cfg struct {
		Runtime struct {
			Command []string `json:"command,omitempty"`
		} `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(doc.StatefulGroup.Recipe.Config, &cfg); err != nil {
		t.Fatalf("decode recipe config: %v\n%s", err, string(doc.StatefulGroup.Recipe.Config))
	}
	if len(cfg.Runtime.Command) != 3 || cfg.Runtime.Command[2] == "" {
		t.Fatalf("stateful command = %#v, want non-empty shell script", cfg.Runtime.Command)
	}
}
