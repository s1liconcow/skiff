package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/tui"
)

type tuiOutput struct {
	OK        bool          `json:"ok"`
	TraceID   string        `json:"trace_id,omitempty"`
	Dashboard tui.Dashboard `json:"dashboard"`
}

var (
	newTUIClient  = client.New
	newTUIProgram = func(model tea.Model, out io.Writer) tuiProgram {
		return tea.NewProgram(model, tea.WithOutput(out))
	}
)

type tuiProgram interface {
	Run() (tea.Model, error)
}

func runTUI(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name to preselect/filter")
	fresh := fs.Bool("fresh", false, "bypass cached API views where supported")
	readOnly := fs.Bool("read-only", false, "disable mutating TUI actions")
	once := fs.Bool("once", false, "render one frame and exit")

	flagArgs, positionals, err := splitTUIArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "tui", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "tui", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "tui", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newTUIClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "tui", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	model := tui.New(tui.Options{
		Client:   skiffClient,
		Service:  *service,
		TraceID:  *flags.traceID,
		Fresh:    *fresh,
		ReadOnly: *readOnly,
		NoColor:  *flags.noColor || *flags.format == "json",
	})
	if *once || *flags.format == "json" {
		loadedModel, err := model.Load(nilContext())
		if err != nil {
			return writeClientError(binary, "tui", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		switch *flags.format {
		case "human", "text":
			_, _ = io.WriteString(stdout, loadedModel.View())
			return ExitSuccess
		case "json":
			if err := json.NewEncoder(stdout).Encode(tuiOutput{OK: true, TraceID: *flags.traceID, Dashboard: loadedModel.Dashboard()}); err != nil {
				fmt.Fprintf(stderr, "%s tui: %v\n", binary, err)
				return ExitInternalError
			}
			return ExitSuccess
		default:
			return writeClientCommandError(binary, "tui", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
		}
	}
	if *flags.format != "human" && *flags.format != "text" {
		return writeClientCommandError(binary, "tui", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
	if _, err := newTUIProgram(model, stdout).Run(); err != nil {
		return writeClientError(binary, "tui", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return ExitSuccess
}

func splitTUIArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
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
