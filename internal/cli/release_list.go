package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type releaseListOutput struct {
	OK       bool               `json:"ok"`
	TraceID  string             `json:"trace_id,omitempty"`
	Service  string             `json:"service"`
	Releases []releaseListEntry `json:"releases"`
}

type releaseListEntry struct {
	Key     string                 `json:"key"`
	Release schema.ReleaseManifest `json:"release"`
}

var openReleaseObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) {
	return client.OpenObjectStore(cfg)
}

func runReleaseList(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" release list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	limit := fs.Int("limit", 0, "maximum releases to return")

	flagArgs, positionals, err := splitReleaseListArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "release-list", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "release-list", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "release-list", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "release-list", *flags.format, *flags.traceID, errors.New("release list currently requires --direct mode"), stdout, stderr)
	}
	store, err := openReleaseObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "release-list", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	docs, err := (release.Manager{Store: store}).ListManifests(nilContext(), release.ManifestListOptions{Service: *service, Limit: *limit})
	if err != nil {
		return writeClientCommandError(binary, "release-list", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	entries := make([]releaseListEntry, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, releaseListEntry{Key: doc.Key, Release: doc.Manifest})
	}
	switch *flags.format {
	case "human", "text":
		for _, entry := range entries {
			manifest := entry.Release
			fmt.Fprintf(stdout, "%s %s env=%s", manifest.Service, manifest.ReleaseID, manifest.Env)
			if manifest.CreatedAt != "" {
				fmt.Fprintf(stdout, " created=%s", manifest.CreatedAt)
			}
			if manifest.Artifact.Type != "" {
				fmt.Fprintf(stdout, " artifact=%s", manifest.Artifact.Type)
			}
			if manifest.Artifact.Digest != "" {
				fmt.Fprintf(stdout, " digest=%s", manifest.Artifact.Digest)
			}
			if len(manifest.Signatures) > 0 {
				fmt.Fprintf(stdout, " signatures=%d", len(manifest.Signatures))
			}
			fmt.Fprintln(stdout)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(releaseListOutput{OK: true, TraceID: *flags.traceID, Service: *service, Releases: entries}); err != nil {
			fmt.Fprintf(stderr, "%s release list: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "release-list", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitReleaseListArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"limit":        true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}
