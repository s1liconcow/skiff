package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type LoadOptions struct {
	ModeDefault  Mode
	ConfigPath   string
	UserDataPath string
	Env          map[string]string
	Overrides    map[string]string
}

func Load(opts LoadOptions) (Loaded, error) {
	env := opts.Env
	if env == nil {
		env = environ()
	}

	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = env["SKIFF_CONFIG"]
	}

	loaded := Defaults(opts.ModeDefault)
	if configPath != "" {
		values, err := parseConfigFile(configPath)
		if err != nil {
			return Loaded{}, err
		}
		applyValues(&loaded, values, "file:"+configPath)
	}
	if opts.UserDataPath != "" {
		data, err := os.ReadFile(opts.UserDataPath)
		if err != nil {
			return Loaded{}, fmt.Errorf("read runner user-data %q: %w", opts.UserDataPath, err)
		}
		userData, err := ParseRunnerUserData(data)
		if err != nil {
			return Loaded{}, err
		}
		applyConfig(&loaded, userData, "user-data:"+opts.UserDataPath)
	}

	applyValues(&loaded, valuesFromEnv(env), "env")
	applyValues(&loaded, opts.Overrides, "flag")

	return loaded, nil
}

func applyConfig(loaded *Loaded, cfg Config, source string) {
	values := map[string]string{
		FieldEnv:         cfg.Env,
		FieldProvider:    cfg.Provider,
		FieldRegion:      cfg.Region,
		FieldStateBucket: cfg.StateBucket,
		FieldKMSKey:      cfg.KMSKey,
		FieldAuthMode:    cfg.AuthMode,
		FieldLogLevel:    cfg.LogLevel,
		FieldMode:        string(cfg.Mode),
		FieldAPIURL:      cfg.APIURL,
		FieldService:     cfg.Service,
		FieldControlKey:  cfg.ControlKey,
	}
	applyValues(loaded, values, source)
}

func applyValues(loaded *Loaded, values map[string]string, source string) {
	if loaded.Sources == nil {
		loaded.Sources = make(map[string]string)
	}
	for field, value := range values {
		if value == "" {
			continue
		}
		switch field {
		case FieldEnv:
			loaded.Config.Env = value
		case FieldProvider:
			loaded.Config.Provider = value
		case FieldRegion:
			loaded.Config.Region = value
		case FieldStateBucket:
			loaded.Config.StateBucket = value
		case FieldKMSKey:
			loaded.Config.KMSKey = value
		case FieldAuthMode:
			loaded.Config.AuthMode = value
		case FieldLogLevel:
			loaded.Config.LogLevel = value
		case FieldMode:
			loaded.Config.Mode = Mode(value)
		case FieldAPIURL:
			loaded.Config.APIURL = value
		case FieldService:
			loaded.Config.Service = value
		case FieldControlKey:
			loaded.Config.ControlKey = value
		default:
			continue
		}
		loaded.Sources[field] = source
	}
}

func parseConfigFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return parseJSONMap(body, "config "+path)
	case ".yaml", ".yml", "":
		return parseFlatYAML(body, "config "+path)
	default:
		return nil, fmt.Errorf("config %q: unsupported extension; expected .json, .yaml, or .yml", path)
	}
}

func parseJSONMap(body []byte, source string) (map[string]string, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: parse JSON: %w", source, err)
	}
	out := make(map[string]string, len(raw))
	for key, rawValue := range raw {
		field, err := normalizeFileField(key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		var value string
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, fmt.Errorf("%s: field %q must be a string", source, key)
		}
		out[field] = strings.TrimSpace(value)
	}
	return out, nil
}

func parseFlatYAML(body []byte, source string) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "-") || strings.Contains(line, "{") || strings.Contains(line, "[") {
			return nil, fmt.Errorf("%s:%d: only flat key/value YAML is supported", source, lineNo)
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key: value", source, lineNo)
		}
		field, err := normalizeFileField(strings.TrimSpace(key))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", source, lineNo, err)
		}
		out[field] = trimYAMLValue(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read YAML: %w", source, err)
	}
	return out, nil
}

func trimYAMLValue(value string) string {
	value = strings.TrimSpace(value)
	if hash := strings.Index(value, " #"); hash >= 0 {
		value = strings.TrimSpace(value[:hash])
	}
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func normalizeFileField(key string) (string, error) {
	switch key {
	case "env":
		return FieldEnv, nil
	case "provider":
		return FieldProvider, nil
	case "region":
		return FieldRegion, nil
	case "state_bucket", "stateBucket":
		return FieldStateBucket, nil
	case "kms_key", "kmsKey":
		return FieldKMSKey, nil
	case "auth_mode", "authMode":
		return FieldAuthMode, nil
	case "log_level", "logLevel":
		return FieldLogLevel, nil
	case "mode":
		return FieldMode, nil
	case "api_url", "apiURL", "apiUrl":
		return FieldAPIURL, nil
	case "service":
		return FieldService, nil
	case "control_key", "controlKey":
		return FieldControlKey, nil
	default:
		return "", fmt.Errorf("unknown field %q", key)
	}
}

func valuesFromEnv(env map[string]string) map[string]string {
	return map[string]string{
		FieldEnv:         env["SKIFF_ENV"],
		FieldProvider:    env["SKIFF_PROVIDER"],
		FieldRegion:      env["SKIFF_REGION"],
		FieldStateBucket: env["SKIFF_STATE_BUCKET"],
		FieldKMSKey:      env["SKIFF_KMS_KEY"],
		FieldAuthMode:    env["SKIFF_AUTH_MODE"],
		FieldLogLevel:    env["SKIFF_LOG_LEVEL"],
		FieldMode:        env["SKIFF_MODE"],
		FieldAPIURL:      env["SKIFF_API_URL"],
		FieldService:     env["SKIFF_SERVICE"],
		FieldControlKey:  env["SKIFF_CONTROL_KEY"],
	}
}

func environ() map[string]string {
	out := make(map[string]string)
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}
