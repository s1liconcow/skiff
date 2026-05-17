package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
)

type rootOptions struct {
	Command string
	Args    []string

	ConfigPath string
	Context    string
	Env        string
	Provider   string
	Region     string
	State      string
	APIURL     string
	Mode       config.Mode
	Format     string
	NoColor    bool
	Yes        bool
	TraceID    string

	configSet   bool
	contextSet  bool
	envSet      bool
	providerSet bool
	regionSet   bool
	stateSet    bool
	apiURLSet   bool
	modeSet     bool
	formatSet   bool
	noColorSet  bool
	yesSet      bool
	traceIDSet  bool
	apiSet      bool
	directSet   bool
}

func parseRootArgs(args []string) (rootOptions, error) {
	root := rootOptions{Format: "human"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 >= len(args) {
				return root, errors.New("command is required after --")
			}
			root.Command = args[i+1]
			root.Args = append([]string(nil), args[i+2:]...)
			return root, validateRootMode(root)
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			root.Command = arg
			root.Args = append([]string(nil), args[i+1:]...)
			return root, validateRootMode(root)
		}
		name, value, hasValue := splitFlag(arg)
		switch name {
		case "help", "h":
			root.Command = "help"
			root.Args = append([]string(nil), args[i+1:]...)
			return root, nil
		case "config":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.ConfigPath = value
			root.configSet = true
		case "context":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.Context = value
			root.contextSet = true
		case "env":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.Env = value
			root.envSet = true
		case "provider":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.Provider = value
			root.providerSet = true
		case "region":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.Region = value
			root.regionSet = true
		case "state", "state-bucket":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.State = value
			root.stateSet = true
		case "api-url":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.APIURL = value
			root.apiURLSet = true
		case "api":
			root.apiSet = true
			root.Mode = config.ModeAPI
			root.modeSet = true
			if hasValue && value != "" && value != "true" && value != "false" {
				root.APIURL = value
				root.apiURLSet = true
			}
			if hasValue && (value == "false" || value == "0") {
				root.apiSet = false
				root.modeSet = false
				root.Mode = ""
			}
		case "direct":
			enabled := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return root, fmt.Errorf("--direct must be a boolean")
				}
				enabled = parsed
			}
			if enabled {
				root.directSet = true
				root.Mode = config.ModeDirect
				root.modeSet = true
			}
		case "format":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.Format = value
			root.formatSet = true
		case "no-color":
			enabled := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return root, fmt.Errorf("--no-color must be a boolean")
				}
				enabled = parsed
			}
			root.NoColor = enabled
			root.noColorSet = true
		case "yes", "y":
			enabled := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return root, fmt.Errorf("--yes must be a boolean")
				}
				enabled = parsed
			}
			root.Yes = enabled
			root.yesSet = true
		case "trace-id":
			if !hasValue {
				i++
				if i >= len(args) {
					return root, fmt.Errorf("missing value for --%s", name)
				}
				value = args[i]
			}
			root.TraceID = value
			root.traceIDSet = true
		default:
			return root, fmt.Errorf("unknown global flag --%s", name)
		}
	}
	return root, validateRootMode(root)
}

func validateRootMode(root rootOptions) error {
	if root.apiSet && root.directSet {
		return errors.New("--api and --direct cannot both be set")
	}
	return nil
}

func splitFlag(arg string) (name string, value string, hasValue bool) {
	name = strings.TrimLeft(arg, "-")
	if before, after, ok := strings.Cut(name, "="); ok {
		return before, after, true
	}
	return name, "", false
}

func (r rootOptions) configOverrides() map[string]string {
	overrides := make(map[string]string)
	if r.envSet {
		overrides[config.FieldEnv] = r.Env
	}
	if r.providerSet {
		overrides[config.FieldProvider] = r.Provider
	}
	if r.regionSet {
		overrides[config.FieldRegion] = r.Region
	}
	if r.stateSet {
		overrides[config.FieldStateBucket] = r.State
	}
	if r.apiURLSet {
		overrides[config.FieldAPIURL] = r.APIURL
	}
	if r.modeSet {
		overrides[config.FieldMode] = string(r.Mode)
	}
	return overrides
}

func writeRootError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "show_help", Command: binary + " help", Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s: %v\n", binary, err)
	return ExitUserError
}

func writeClientError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	code := string(skifferrors.FromClientCode(client.ErrorCode(err)))
	exitCode := client.ExitCode(err)
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{
			OK:      false,
			Code:    code,
			Summary: client.ErrorSummary(err),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_config", Command: binary + " config show --format json", Mutating: false},
			},
		})
		return exitCode
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return exitCode
}
