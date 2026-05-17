package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type deployOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Result  deploy.Result `json:"result"`
}

var newDeployProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
	return newCLIProvider(cfg, store)
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
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	keyID := fs.String("key-id", "local-deploy", "signing key ID")
	signingSeed := fs.String("signing-seed-base64", "", "base64 Ed25519 seed for release signing")
	shadow := fs.Bool("shadow", false, "deploy Skiff infrastructure without attaching public or internal ingress listeners")
	canary := fs.Bool("canary", false, "create and run a staged canary deployment saga")
	canaryStages := fs.String("canary-stages", "5,25,100", "comma-separated canary stages by percent")
	canaryBake := fs.String("canary-bake", templates.DefaultCanaryBake, "canary bake duration")
	canaryRun := fs.Bool("canary-run", true, "run the canary saga after creating it")
	canaryMetric := fs.String("canary-metric", "", "metric gate name for canary stages")
	canaryComparator := fs.String("canary-comparator", "<=", "metric gate comparator for canary stages")
	canaryThreshold := fs.Float64("canary-threshold", 0, "metric gate threshold for canary stages")

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
	if *shadow {
		graph = shadowDeployGraph(graph)
	}
	if *canary {
		stages, err := parseCanaryStages(*canaryStages)
		if err != nil {
			return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
		}
		req := templates.CanaryRequest{
			OperationID:  *operationID,
			Service:      graph.Service,
			Env:          graph.Env,
			ReleaseID:    firstNonEmptyString(*releaseID, "rel_"+events.NewID(time.Now().UTC(), *flags.traceID+graph.Service+"canary")),
			Stages:       stages,
			BakeDuration: *canaryBake,
			Actor:        schema.Actor{ID: "skiff-cli", Type: "user"},
			TraceID:      *flags.traceID,
		}
		if *canaryMetric != "" {
			req.MetricGates = []templates.MetricGate{{Metric: *canaryMetric, Comparator: *canaryComparator, Threshold: *canaryThreshold}}
		}
		if *dryRun || *planOnly {
			req = templates.NormalizeCanaryRequest(req)
			result := canarySagaResult{
				SagaID:       req.SagaID,
				OperationID:  req.OperationID,
				Service:      req.Service,
				Env:          req.Env,
				ReleaseID:    req.ReleaseID,
				Status:       schema.SagaPending,
				Stage:        req.Stages[0].Percent,
				NextAction:   "create_saga",
				CurrentSteps: []string{"preflight"},
			}
			return writeCanarySagaResult(binary, "deploy --canary", *flags.format, *flags.traceID, result, stdout, stderr)
		}
		store, err := openSagaObjectStore(loaded.Config)
		if err != nil {
			return writeClientError(binary, "deploy", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		signer, err := signerFromSeed(*keyID, *signingSeed)
		if err != nil {
			return writeSpecError(binary, "DEPLOY_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
		}
		published, err := (deploy.Deployer{Store: store, Signer: signer}).PublishRelease(nilContext(), graph, deploy.Request{
			Actor:       req.Actor,
			TraceID:     req.TraceID,
			ReleaseID:   req.ReleaseID,
			OperationID: req.OperationID,
		})
		if err != nil {
			return writeSpecError(binary, "DEPLOY_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
		}
		req.ReleaseID = published.ReleaseID
		req.OperationID = published.OperationID
		req.TraceID = published.TraceID
		result, err := createAndMaybeRunCanary(nilContext(), binary, loaded.Config, req, *canaryRun)
		if err != nil {
			return writeClientError(binary, "deploy", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		return writeCanarySagaResult(binary, "deploy --canary", *flags.format, *flags.traceID, *result, stdout, stderr)
	}

	var storeNeeded = !*dryRun && !*planOnly
	var storeErr error
	var store objstore.ObjectStore
	if storeNeeded {
		store, storeErr = client.OpenObjectStore(loaded.Config)
		if storeErr != nil {
			return writeClientError(binary, "deploy", *flags.format, *flags.traceID, storeErr, stdout, stderr)
		}
	}
	cloud, err := newDeployProvider(loaded.Config, store)
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
		Provider: cloud,
		Signer:   signer,
	}.Deploy(nilContext(), graph, deploy.Request{
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     *flags.traceID,
		ReleaseID:   *releaseID,
		OperationID: *operationID,
		ApprovalID:  *approvalID,
		DryRun:      *dryRun,
		PlanOnly:    *planOnly,
		Shadow:      *shadow,
	})
	if err != nil {
		return writeSpecError(binary, "DEPLOY_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}

	switch *flags.format {
	case "human", "text":
		if result.DryRun || result.PlanOnly {
			fmt.Fprintf(stdout, "deploy plan for %s/%s:\n", result.Plan.Env, result.Plan.Service)
			if result.Shadow {
				fmt.Fprintln(stdout, "shadow: ingress listeners omitted")
			}
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
		"api-url":                              true,
		"approval-id":                          true,
		"aws-live-apply":                       false,
		"aws-vpc-id":                           true,
		"aws-subnet-ids":                       true,
		"aws-ami-id":                           true,
		"aws-alb-listener-arn":                 true,
		"aws-load-balancer-security-group-ref": true,
		"canary-bake":                          true,
		"canary-comparator":                    true,
		"canary-metric":                        true,
		"canary-stages":                        true,
		"canary-threshold":                     true,
		"config":                               true,
		"env":                                  true,
		"file":                                 true,
		"format":                               true,
		"key-id":                               true,
		"mode":                                 true,
		"operation-id":                         true,
		"provider":                             true,
		"region":                               true,
		"release-id":                           true,
		"shadow":                               false,
		"signing-seed-base64":                  true,
		"state":                                true,
		"state-bucket":                         true,
		"trace-id":                             true,
	}
	return splitArgs(args, valueFlags)
}

func shadowDeployGraph(graph *ir.Graph) *ir.Graph {
	if graph == nil {
		return nil
	}
	copyGraph := *graph
	copyGraph.Resources = graph.Resources
	copyGraph.Resources.Listeners = nil
	return &copyGraph
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
