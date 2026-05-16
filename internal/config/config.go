package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type Mode string

const (
	ModeAPI    Mode = "api"
	ModeDirect Mode = "direct"
	ModeSkiffd Mode = "skiffd"
	ModeRunner Mode = "runner"
)

const (
	FieldEnv         = "env"
	FieldProvider    = "provider"
	FieldRegion      = "region"
	FieldStateBucket = "state_bucket"
	FieldKMSKey      = "kms_key"
	FieldAuthMode    = "auth_mode"
	FieldLogLevel    = "log_level"
	FieldMode        = "mode"
	FieldAPIURL      = "api_url"
	FieldService     = "service"
	FieldControlKey  = "control_key"
)

type Config struct {
	Env         string `json:"env,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Region      string `json:"region,omitempty"`
	StateBucket string `json:"state_bucket,omitempty"`
	KMSKey      string `json:"kms_key,omitempty"`
	AuthMode    string `json:"auth_mode,omitempty"`
	LogLevel    string `json:"log_level,omitempty"`
	Mode        Mode   `json:"mode,omitempty"`
	APIURL      string `json:"api_url,omitempty"`
	Service     string `json:"service,omitempty"`
	ControlKey  string `json:"control_key,omitempty"`
}

type Loaded struct {
	Config  Config            `json:"config"`
	Sources map[string]string `json:"sources,omitempty"`
}

func Defaults(mode Mode) Loaded {
	if mode == "" {
		mode = ModeAPI
	}
	return Loaded{
		Config: Config{
			Mode:     mode,
			AuthMode: "none",
			LogLevel: "info",
		},
		Sources: map[string]string{
			FieldMode:     "default",
			FieldAuthMode: "default",
			FieldLogLevel: "default",
		},
	}
}

func (l Loaded) Redacted() Loaded {
	out := Loaded{
		Config:  l.Config,
		Sources: make(map[string]string, len(l.Sources)),
	}
	for field, source := range l.Sources {
		out.Sources[field] = source
	}
	return out
}

func Validate(loaded Loaded) error {
	cfg := loaded.Config
	var fields []FieldError

	switch cfg.Mode {
	case ModeAPI:
		require(&fields, loaded.Sources, FieldAPIURL, cfg.APIURL)
	case ModeDirect:
		require(&fields, loaded.Sources, FieldEnv, cfg.Env)
		require(&fields, loaded.Sources, FieldProvider, cfg.Provider)
		require(&fields, loaded.Sources, FieldRegion, cfg.Region)
		require(&fields, loaded.Sources, FieldStateBucket, cfg.StateBucket)
	case ModeSkiffd:
		require(&fields, loaded.Sources, FieldEnv, cfg.Env)
		require(&fields, loaded.Sources, FieldProvider, cfg.Provider)
		require(&fields, loaded.Sources, FieldRegion, cfg.Region)
		require(&fields, loaded.Sources, FieldStateBucket, cfg.StateBucket)
	case ModeRunner:
		require(&fields, loaded.Sources, FieldEnv, cfg.Env)
		require(&fields, loaded.Sources, FieldProvider, cfg.Provider)
		require(&fields, loaded.Sources, FieldRegion, cfg.Region)
		require(&fields, loaded.Sources, FieldStateBucket, cfg.StateBucket)
		require(&fields, loaded.Sources, FieldService, cfg.Service)
		require(&fields, loaded.Sources, FieldControlKey, cfg.ControlKey)
	default:
		fields = append(fields, FieldError{
			Field:   FieldMode,
			Source:  sourceFor(loaded.Sources, FieldMode),
			Code:    "UNSUPPORTED_MODE",
			Message: fmt.Sprintf("unsupported mode %q; expected api, direct, skiffd, or runner", cfg.Mode),
		})
	}

	if cfg.StateBucket != "" {
		validateStateBucket(&fields, loaded.Sources, cfg.StateBucket)
	}
	if cfg.APIURL != "" {
		validateHTTPURL(&fields, loaded.Sources, FieldAPIURL, cfg.APIURL)
	}
	if cfg.LogLevel != "" {
		switch cfg.LogLevel {
		case "debug", "info", "warn", "error":
		default:
			fields = append(fields, FieldError{
				Field:   FieldLogLevel,
				Source:  sourceFor(loaded.Sources, FieldLogLevel),
				Code:    "INVALID_LOG_LEVEL",
				Message: "log level must be one of debug, info, warn, or error",
			})
		}
	}
	if cfg.Provider != "" {
		validateName(&fields, loaded.Sources, FieldProvider, cfg.Provider)
	}
	if cfg.Env != "" {
		validateName(&fields, loaded.Sources, FieldEnv, cfg.Env)
	}
	if cfg.Service != "" {
		validateName(&fields, loaded.Sources, FieldService, cfg.Service)
	}

	if len(fields) > 0 {
		return ValidationError{Fields: fields}
	}
	return nil
}

type FieldError struct {
	Field   string `json:"field"`
	Source  string `json:"source"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationError struct {
	Fields []FieldError `json:"fields"`
}

func (e ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "config validation failed"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s from %s: %s", field.Field, field.Source, field.Message))
	}
	return "config validation failed: " + strings.Join(parts, "; ")
}

func require(fields *[]FieldError, sources map[string]string, field, value string) {
	if strings.TrimSpace(value) != "" {
		return
	}
	*fields = append(*fields, FieldError{
		Field:   field,
		Source:  sourceFor(sources, field),
		Code:    "REQUIRED",
		Message: "required for selected mode",
	})
}

func validateStateBucket(fields *[]FieldError, sources map[string]string, value string) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" {
		*fields = append(*fields, FieldError{
			Field:   FieldStateBucket,
			Source:  sourceFor(sources, FieldStateBucket),
			Code:    "INVALID_URI",
			Message: "state bucket must be an absolute URI such as s3://skiff-state-prod",
		})
		return
	}
	switch u.Scheme {
	case "s3", "file", "memory":
	default:
		*fields = append(*fields, FieldError{
			Field:   FieldStateBucket,
			Source:  sourceFor(sources, FieldStateBucket),
			Code:    "UNSUPPORTED_URI_SCHEME",
			Message: "state bucket scheme must be s3, file, or memory",
		})
	}
}

func validateHTTPURL(fields *[]FieldError, sources map[string]string, field, value string) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		*fields = append(*fields, FieldError{
			Field:   field,
			Source:  sourceFor(sources, field),
			Code:    "INVALID_URL",
			Message: "must be an absolute http or https URL",
		})
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		*fields = append(*fields, FieldError{
			Field:   field,
			Source:  sourceFor(sources, field),
			Code:    "UNSUPPORTED_URL_SCHEME",
			Message: "URL scheme must be http or https",
		})
	}
}

func validateName(fields *[]FieldError, sources map[string]string, field, value string) {
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		*fields = append(*fields, FieldError{
			Field:   field,
			Source:  sourceFor(sources, field),
			Code:    "INVALID_NAME",
			Message: "must contain only lowercase letters, digits, and hyphens",
		})
		return
	}
}

func sourceFor(sources map[string]string, field string) string {
	if sources == nil {
		return "unset"
	}
	if source, ok := sources[field]; ok {
		return source
	}
	return "unset"
}

func FieldNames() []string {
	names := []string{
		FieldEnv,
		FieldProvider,
		FieldRegion,
		FieldStateBucket,
		FieldKMSKey,
		FieldAuthMode,
		FieldLogLevel,
		FieldMode,
		FieldAPIURL,
		FieldService,
		FieldControlKey,
	}
	sort.Strings(names)
	return names
}
