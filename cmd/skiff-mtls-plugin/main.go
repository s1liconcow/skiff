package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/s1liconcow/skiff/internal/plugins/mtls"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	body, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("expected plugin hook envelope on stdin")
	}
	var envelope struct {
		Hook    pluginapi.Hook  `json:"hook"`
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode plugin hook envelope: %w", err)
	}
	response, err := mtls.Handle(context.Background(), envelope.Hook, envelope.Request)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(response)
}
