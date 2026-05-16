package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type deployOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Result  deploy.Result `json:"result"`
}

func runDeploy(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" deploy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	dryRun := fs.Bool("dry-run", false, "plan deploy without writing object state")
	planOnly := fs.Bool("plan-only", false, "render deploy plan without writing object state")
	releaseID := fs.String("release-id", "", "release ID to publish")
	operationID := fs.String("operation-id", "", "operation ID to use")
	keyID := fs.String("key-id", "local-deploy", "signing key ID")
	signingSeed := fs.String("signing-seed-base64", "", "base64 Ed25519 seed for release signing")

	flagArgs, positionals, err := splitDeployArgs(args)
	if err != nil {
		return writeSpecError(binary, "DEPLOY_INVALID", defaultString(root.Format, "human"), root.TraceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, errors.New("spec file is required"), nil, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, errors.New("deploy currently requires --direct mode"), nil, stdout, stderr)
	}
	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *flags.format, *flags.traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}

	var storeOpts []aws.Option
	var storeNeeded = !*dryRun && !*planOnly
	var storeErr error
	var store objstore.ObjectStore
	if storeNeeded {
		store, storeErr = client.OpenObjectStore(loaded.Config)
		if storeErr != nil {
			return writeClientError(binary, "deploy", *flags.format, *flags.traceID, storeErr, stdout, stderr)
		}
		storeOpts = append(storeOpts, aws.WithStateStore(store))
	}
	awsProvider, err := aws.NewFromConfig(loaded.Config, storeOpts...)
	if err != nil {
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	var signer signing.Signer
	if storeNeeded {
		signer, err = signerFromSeed(*keyID, *signingSeed)
		if err != nil {
			return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
		}
	}
	result, err := deploy.Deployer{
		Store:    store,
		Provider: awsProvider,
		Signer:   signer,
	}.Deploy(nilContext(), graph, deploy.Request{
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     *flags.traceID,
		ReleaseID:   *releaseID,
		OperationID: *operationID,
		DryRun:      *dryRun,
		PlanOnly:    *planOnly,
	})
	if err != nil {
		return writeSpecError(binary, "DEPLOY_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}

	switch *flags.format {
	case "human", "text":
		if result.DryRun || result.PlanOnly {
			fmt.Fprintf(stdout, "deploy plan for %s/%s:\n", result.Plan.Env, result.Plan.Service)
			for _, resource := range result.Plan.Resources {
				fmt.Fprintf(stdout, "- %s %s %s\n", resource.Action, resource.Kind, resource.Name)
			}
			return ExitSuccess
		}
		fmt.Fprintf(stdout, "deploy %s succeeded\n", result.OperationID)
		fmt.Fprintf(stdout, "release: %s\n", result.ReleaseID)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(deployOutput{OK: result.OK, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s deploy: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), nil, stdout, stderr)
	}
}

func splitDeployArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":             true,
		"config":              true,
		"env":                 true,
		"file":                true,
		"format":              true,
		"key-id":              true,
		"mode":                true,
		"operation-id":        true,
		"provider":            true,
		"region":              true,
		"release-id":          true,
		"signing-seed-base64": true,
		"state":               true,
		"state-bucket":        true,
		"trace-id":            true,
	}
	return splitArgs(args, valueFlags)
}

func signerFromSeed(keyID, seedValue string) (signing.Signer, error) {
	if seedValue == "" {
		return nil, errors.New("--signing-seed-base64 is required for deploy")
	}
	seed, err := base64.StdEncoding.DecodeString(seedValue)
	if err != nil {
		return nil, fmt.Errorf("--signing-seed-base64 must be base64: %w", err)
	}
	return signing.NewLocalSignerFromSeed(keyID, seed)
}
