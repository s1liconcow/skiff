package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/buildinfo"
)

const (
	ExitSuccess   = 0
	ExitUserError = 1
)

type versionOutput struct {
	OK        bool   `json:"ok"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	TraceID   string `json:"trace_id,omitempty"`
}

func Run(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr, binary)
		return ExitUserError
	}

	switch args[0] {
	case "version":
		if err := runVersion(binary, args[1:], stdout); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	case "help", "-h", "--help":
		printUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", binary, args[0])
		printUsage(stderr, binary)
		return ExitUserError
	}
}

func runVersion(binary string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(binary+" version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	_ = noColor
	_ = yes

	info := buildinfo.Current(binary)
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s version %s\n", info.Binary, info.Version)
		fmt.Fprintf(stdout, "commit: %s\n", info.Commit)
		fmt.Fprintf(stdout, "build_date: %s\n", info.BuildDate)
		if *traceID != "" {
			fmt.Fprintf(stdout, "trace_id: %s\n", *traceID)
		}
		return nil
	case "json":
		enc := json.NewEncoder(stdout)
		return enc.Encode(versionOutput{
			OK:        true,
			Binary:    info.Binary,
			Version:   info.Version,
			Commit:    info.Commit,
			BuildDate: info.BuildDate,
			TraceID:   *traceID,
		})
	default:
		return errors.New(`unsupported format; expected "human" or "json"`)
	}
}

func printUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version    Print version, commit, and build date")
}
