package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/cli"
	"github.com/s1liconcow/skiff/internal/config"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/skiffd"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(runServe(os.Args[2:], os.Stdout, os.Stderr))
	}
	os.Exit(cli.Run("skiffd", os.Args[1:], os.Stdout, os.Stderr))
}

type serveStartOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Addr    string        `json:"addr"`
	Config  config.Config `json:"config"`
}

type serveErrorOutput struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Summary string `json:"summary"`
	TraceID string `json:"trace_id,omitempty"`
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skiffd serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	addr := fs.String("addr", "127.0.0.1:8585", "listen address")
	format := fs.String("format", "human", "startup output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", "", "path to Skiff config file")

	fs.String("env", "", "Skiff environment name")
	fs.String("provider", "", "cloud provider name")
	fs.String("region", "", "cloud provider region")
	fs.String("state-bucket", "", "object-state bucket URI")
	fs.String("state", "", "alias for --state-bucket")
	fs.String("auth-mode", "", "auth mode")
	fs.String("log-level", "", "log level")
	fs.String("mode", "", "config mode; must be skiffd for serve")

	if err := fs.Parse(args); err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeServeError(*format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	if *format != "human" && *format != "text" && *format != "json" {
		return writeServeError(*format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := map[string]string{}
	flagToField := map[string]string{
		"env":          config.FieldEnv,
		"provider":     config.FieldProvider,
		"region":       config.FieldRegion,
		"state-bucket": config.FieldStateBucket,
		"state":        config.FieldStateBucket,
		"auth-mode":    config.FieldAuthMode,
		"log-level":    config.FieldLogLevel,
		"mode":         config.FieldMode,
	}
	fs.Visit(func(f *flag.Flag) {
		if field := flagToField[f.Name]; field != "" {
			overrides[field] = f.Value.String()
		}
	})

	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: config.ModeSkiffd,
		ConfigPath:  *configPath,
		Overrides:   overrides,
	})
	if err == nil {
		applyLocalServeDefaults(&loaded)
		err = config.Validate(loaded)
	}
	if err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeSkiffd {
		return writeServeError(*format, *traceID, fmt.Errorf("mode %q cannot be served by skiffd; use mode skiffd", loaded.Config.Mode), stdout, stderr)
	}
	if !strings.HasPrefix(loaded.Config.StateBucket, "memory://") {
		return writeServeError(*format, *traceID, errors.New("skiffd serve currently supports memory:// state buckets for local skeleton startup"), stdout, stderr)
	}

	store := memory.New()
	idx, err := stateindex.New(store, stateindex.Options{Clock: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	if _, err := idx.Rebuild(context.Background()); err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	server, err := skiffd.New(skiffd.Options{
		Config:      loaded.Config,
		ObjectStore: store,
		Index:       idx,
		BuildInfo:   buildinfo.Current("skiffd"),
		Logger:      logger,
	})
	if err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}

	publicAddr := "http://" + listener.Addr().String()
	redacted := loaded.Redacted().Config
	redacted.StateBucket = redactURI(redacted.StateBucket)
	if *format == "json" {
		if err := json.NewEncoder(stdout).Encode(serveStartOutput{
			OK:      true,
			TraceID: *traceID,
			Addr:    publicAddr,
			Config:  redacted,
		}); err != nil {
			_ = listener.Close()
			return writeServeError(*format, *traceID, err, stdout, stderr)
		}
	} else {
		fmt.Fprintf(stdout, "skiffd listening on %s\n", publicAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := idx.RunRefreshLoop(ctx, 30*time.Second); err != nil && !errors.Is(err, context.Canceled) {
			logger.WarnContext(ctx, "skiffd index refresh loop stopped", "error", err)
		}
	}()
	if err := server.Serve(ctx, listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return writeServeError(*format, *traceID, err, stdout, stderr)
	}
	return cli.ExitSuccess
}

func applyLocalServeDefaults(loaded *config.Loaded) {
	if loaded.Sources == nil {
		loaded.Sources = make(map[string]string)
	}
	defaultString := func(field string, value string, set func()) {
		if strings.TrimSpace(value) != "" {
			return
		}
		set()
		loaded.Sources[field] = "default"
	}
	defaultString(config.FieldEnv, loaded.Config.Env, func() { loaded.Config.Env = "local" })
	defaultString(config.FieldProvider, loaded.Config.Provider, func() { loaded.Config.Provider = "aws" })
	defaultString(config.FieldRegion, loaded.Config.Region, func() { loaded.Config.Region = "local" })
	defaultString(config.FieldStateBucket, loaded.Config.StateBucket, func() { loaded.Config.StateBucket = "memory://skiffd-local" })
}

func writeServeError(format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(serveErrorOutput{
			OK:      false,
			Code:    "SKIFFD_SERVE_FAILED",
			Summary: err.Error(),
			TraceID: traceID,
		})
		return cli.ExitUserError
	}
	fmt.Fprintf(stderr, "skiffd serve: %v\n", err)
	return cli.ExitUserError
}

func redactURI(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	return parsed.String()
}
