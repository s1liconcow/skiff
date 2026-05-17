package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigFilename = ".skiffconfig"
	EnvConfigPath         = "SKIFF_CONFIG"
	EnvContext            = "SKIFF_CONTEXT"

	SkiffConfigAPIVersion = "skiff.dev/v1alpha1"
	SkiffConfigKind       = "SkiffConfig"
)

type SkiffConfigFile struct {
	APIVersion          string         `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind                string         `json:"kind,omitempty" yaml:"kind,omitempty"`
	CurrentContext      string         `json:"current-context,omitempty" yaml:"current-context,omitempty"`
	CurrentContextCamel string         `json:"currentContext,omitempty" yaml:"currentContext,omitempty"`
	CurrentContextSnake string         `json:"current_context,omitempty" yaml:"current_context,omitempty"`
	Contexts            []NamedContext `json:"contexts,omitempty" yaml:"contexts,omitempty"`
}

type NamedContext struct {
	Name    string        `json:"name" yaml:"name"`
	Context ContextConfig `json:"context" yaml:"context"`
}

type ContextConfig struct {
	Mode                                 Mode     `json:"mode,omitempty" yaml:"mode,omitempty"`
	Env                                  string   `json:"env,omitempty" yaml:"env,omitempty"`
	Provider                             string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	Region                               string   `json:"region,omitempty" yaml:"region,omitempty"`
	State                                string   `json:"state,omitempty" yaml:"state,omitempty"`
	StateBucket                          string   `json:"stateBucket,omitempty" yaml:"stateBucket,omitempty"`
	StateBucketSnake                     string   `json:"state_bucket,omitempty" yaml:"state_bucket,omitempty"`
	KMSKey                               string   `json:"kmsKey,omitempty" yaml:"kmsKey,omitempty"`
	KMSKeySnake                          string   `json:"kms_key,omitempty" yaml:"kms_key,omitempty"`
	AuthMode                             string   `json:"authMode,omitempty" yaml:"authMode,omitempty"`
	AuthModeSnake                        string   `json:"auth_mode,omitempty" yaml:"auth_mode,omitempty"`
	LogLevel                             string   `json:"logLevel,omitempty" yaml:"logLevel,omitempty"`
	LogLevelSnake                        string   `json:"log_level,omitempty" yaml:"log_level,omitempty"`
	APIURL                               string   `json:"apiURL,omitempty" yaml:"apiURL,omitempty"`
	APIURLSnake                          string   `json:"api_url,omitempty" yaml:"api_url,omitempty"`
	APIUrl                               string   `json:"apiUrl,omitempty" yaml:"apiUrl,omitempty"`
	Service                              string   `json:"service,omitempty" yaml:"service,omitempty"`
	ControlKey                           string   `json:"controlKey,omitempty" yaml:"controlKey,omitempty"`
	ControlKeySnake                      string   `json:"control_key,omitempty" yaml:"control_key,omitempty"`
	ReleaseID                            string   `json:"releaseID,omitempty" yaml:"releaseID,omitempty"`
	ReleaseIDSnake                       string   `json:"release_id,omitempty" yaml:"release_id,omitempty"`
	AWSLiveApply                         bool     `json:"awsLiveApply,omitempty" yaml:"awsLiveApply,omitempty"`
	AWSVPCID                             string   `json:"awsVPCID,omitempty" yaml:"awsVPCID,omitempty"`
	AWSVPCIDSnake                        string   `json:"aws_vpc_id,omitempty" yaml:"aws_vpc_id,omitempty"`
	AWSSubnetIDs                         []string `json:"awsSubnetIDs,omitempty" yaml:"awsSubnetIDs,omitempty"`
	AWSSubnetIDsSnake                    []string `json:"aws_subnet_ids,omitempty" yaml:"aws_subnet_ids,omitempty"`
	AWSAMIID                             string   `json:"awsAMIID,omitempty" yaml:"awsAMIID,omitempty"`
	AWSAMIIDSnake                        string   `json:"aws_ami_id,omitempty" yaml:"aws_ami_id,omitempty"`
	AWSALBListenerARN                    string   `json:"awsALBListenerARN,omitempty" yaml:"awsALBListenerARN,omitempty"`
	AWSALBListenerARNSnake               string   `json:"aws_alb_listener_arn,omitempty" yaml:"aws_alb_listener_arn,omitempty"`
	AWSLoadBalancerSecurityGroupRef      string   `json:"awsLoadBalancerSecurityGroupRef,omitempty" yaml:"awsLoadBalancerSecurityGroupRef,omitempty"`
	AWSLoadBalancerSecurityGroupRefSnake string   `json:"aws_load_balancer_security_group_ref,omitempty" yaml:"aws_load_balancer_security_group_ref,omitempty"`
}

type ContextSummary struct {
	Name        string `json:"name"`
	Current     bool   `json:"current,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Env         string `json:"env,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Region      string `json:"region,omitempty"`
	StateBucket string `json:"state_bucket,omitempty"`
	APIURL      string `json:"api_url,omitempty"`
}

func ResolveConfigPath(explicit string, env map[string]string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if env == nil {
		env = environ()
	}
	if value := strings.TrimSpace(env[EnvConfigPath]); value != "" {
		return value
	}
	if _, err := os.Stat(DefaultConfigFilename); err == nil {
		return DefaultConfigFilename
	}
	return ""
}

func ResolveContext(explicit string, env map[string]string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if env == nil {
		env = environ()
	}
	return strings.TrimSpace(env[EnvContext])
}

func LoadSkiffConfigFile(path string) (*SkiffConfigFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	file, err := ParseSkiffConfigFile(body, "config "+path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func ParseSkiffConfigFile(body []byte, source string) (*SkiffConfigFile, error) {
	var raw SkiffConfigFile
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: parse Skiff config: %w", source, err)
	}
	if raw.APIVersion != "" && raw.APIVersion != SkiffConfigAPIVersion {
		return nil, fmt.Errorf("%s: unsupported apiVersion %q; expected %s", source, raw.APIVersion, SkiffConfigAPIVersion)
	}
	if raw.Kind != "" && raw.Kind != SkiffConfigKind {
		return nil, fmt.Errorf("%s: unsupported kind %q; expected %s", source, raw.Kind, SkiffConfigKind)
	}
	current, err := resolveCurrentContextAliases(raw.CurrentContext, raw.CurrentContextCamel, raw.CurrentContextSnake)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	raw.CurrentContext = current
	raw.CurrentContextCamel = ""
	raw.CurrentContextSnake = ""
	if raw.APIVersion == "" {
		raw.APIVersion = SkiffConfigAPIVersion
	}
	if raw.Kind == "" {
		raw.Kind = SkiffConfigKind
	}
	if len(raw.Contexts) == 0 {
		return nil, fmt.Errorf("%s: contexts are required", source)
	}
	seen := make(map[string]struct{}, len(raw.Contexts))
	for i := range raw.Contexts {
		name := strings.TrimSpace(raw.Contexts[i].Name)
		if name == "" {
			return nil, fmt.Errorf("%s: contexts[%d].name is required", source, i)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("%s: duplicate context %q", source, name)
		}
		seen[name] = struct{}{}
		raw.Contexts[i].Name = name
		if _, err := raw.Contexts[i].Context.values(); err != nil {
			return nil, fmt.Errorf("%s: context %q: %w", source, name, err)
		}
	}
	return &raw, nil
}

func WriteSkiffConfigFile(path string, file *SkiffConfigFile) error {
	if file == nil {
		return errors.New("config file is required")
	}
	copyFile := *file
	if copyFile.APIVersion == "" {
		copyFile.APIVersion = SkiffConfigAPIVersion
	}
	if copyFile.Kind == "" {
		copyFile.Kind = SkiffConfigKind
	}
	copyFile.CurrentContextCamel = ""
	copyFile.CurrentContextSnake = ""
	body, err := yaml.Marshal(&copyFile)
	if err != nil {
		return fmt.Errorf("render config %q: %w", path, err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

func (f *SkiffConfigFile) Current() string {
	if f == nil {
		return ""
	}
	current, _ := resolveCurrentContextAliases(f.CurrentContext, f.CurrentContextCamel, f.CurrentContextSnake)
	return current
}

func (f *SkiffConfigFile) SelectContext(requested string) (string, map[string]string, error) {
	if f == nil {
		return "", nil, errors.New("config file is required")
	}
	name := strings.TrimSpace(requested)
	if name == "" {
		name = f.Current()
	}
	if name == "" && len(f.Contexts) == 1 {
		name = f.Contexts[0].Name
	}
	if name == "" {
		return "", nil, errors.New("current-context is not set; use skiff config use-context <name> or SKIFF_CONTEXT")
	}
	for _, context := range f.Contexts {
		if context.Name != name {
			continue
		}
		values, err := context.Context.values()
		if err != nil {
			return "", nil, err
		}
		return name, values, nil
	}
	return "", nil, fmt.Errorf("context %q not found", name)
}

func (f *SkiffConfigFile) SetCurrentContext(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("context name is required")
	}
	for _, context := range f.Contexts {
		if context.Name == name {
			f.CurrentContext = name
			f.CurrentContextCamel = ""
			f.CurrentContextSnake = ""
			return nil
		}
	}
	return fmt.Errorf("context %q not found", name)
}

func (f *SkiffConfigFile) Summaries(effective string) []ContextSummary {
	if f == nil {
		return nil
	}
	current := strings.TrimSpace(effective)
	if current == "" {
		current = f.Current()
	}
	out := make([]ContextSummary, 0, len(f.Contexts))
	for _, context := range f.Contexts {
		values, _ := context.Context.values()
		out = append(out, ContextSummary{
			Name:        context.Name,
			Current:     context.Name == current,
			Mode:        values[FieldMode],
			Env:         values[FieldEnv],
			Provider:    values[FieldProvider],
			Region:      values[FieldRegion],
			StateBucket: values[FieldStateBucket],
			APIURL:      values[FieldAPIURL],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c ContextConfig) values() (map[string]string, error) {
	values := make(map[string]string)
	addValue(values, FieldMode, string(c.Mode))
	addValue(values, FieldEnv, c.Env)
	addValue(values, FieldProvider, c.Provider)
	addValue(values, FieldRegion, c.Region)
	state, err := singleAliasValue(FieldStateBucket, c.State, c.StateBucket, c.StateBucketSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldStateBucket, state)
	kmsKey, err := singleAliasValue(FieldKMSKey, c.KMSKey, c.KMSKeySnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldKMSKey, kmsKey)
	authMode, err := singleAliasValue(FieldAuthMode, c.AuthMode, c.AuthModeSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAuthMode, authMode)
	logLevel, err := singleAliasValue(FieldLogLevel, c.LogLevel, c.LogLevelSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldLogLevel, logLevel)
	apiURL, err := singleAliasValue(FieldAPIURL, c.APIURL, c.APIUrl, c.APIURLSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAPIURL, apiURL)
	addValue(values, FieldService, c.Service)
	controlKey, err := singleAliasValue(FieldControlKey, c.ControlKey, c.ControlKeySnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldControlKey, controlKey)
	releaseID, err := singleAliasValue(FieldReleaseID, c.ReleaseID, c.ReleaseIDSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldReleaseID, releaseID)
	if c.AWSLiveApply {
		values[FieldAWSLiveApply] = "true"
	}
	awsVPCID, err := singleAliasValue(FieldAWSVPCID, c.AWSVPCID, c.AWSVPCIDSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAWSVPCID, awsVPCID)
	subnets, err := singleStringSliceAliasValue(FieldAWSSubnetIDs, c.AWSSubnetIDs, c.AWSSubnetIDsSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAWSSubnetIDs, strings.Join(subnets, ","))
	awsAMIID, err := singleAliasValue(FieldAWSAMIID, c.AWSAMIID, c.AWSAMIIDSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAWSAMIID, awsAMIID)
	listener, err := singleAliasValue(FieldAWSALBListenerARN, c.AWSALBListenerARN, c.AWSALBListenerARNSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAWSALBListenerARN, listener)
	lbSG, err := singleAliasValue(FieldAWSLoadBalancerSecurityGroupRef, c.AWSLoadBalancerSecurityGroupRef, c.AWSLoadBalancerSecurityGroupRefSnake)
	if err != nil {
		return nil, err
	}
	addValue(values, FieldAWSLoadBalancerSecurityGroupRef, lbSG)
	return values, nil
}

func addValue(values map[string]string, field, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values[field] = value
	}
}

func singleAliasValue(field string, aliases ...string) (string, error) {
	var selected string
	for _, value := range aliases {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if selected != "" && selected != value {
			return "", fmt.Errorf("%s aliases conflict", field)
		}
		selected = value
	}
	return selected, nil
}

func singleStringSliceAliasValue(field string, aliases ...[]string) ([]string, error) {
	var selected []string
	for _, values := range aliases {
		values = trimStringSlice(values)
		if len(values) == 0 {
			continue
		}
		if len(selected) > 0 && strings.Join(selected, "\x00") != strings.Join(values, "\x00") {
			return nil, fmt.Errorf("%s aliases conflict", field)
		}
		selected = values
	}
	return selected, nil
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func resolveCurrentContextAliases(values ...string) (string, error) {
	var selected string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if selected != "" && selected != value {
			return "", errors.New("current-context aliases conflict")
		}
		selected = value
	}
	return selected, nil
}

func looksLikeSkiffConfig(body []byte) bool {
	text := string(body)
	for _, token := range []string{"contexts:", "\"contexts\"", "current-context:", "currentContext:", "current_context:", "\"current-context\"", "\"currentContext\"", "\"current_context\"", "kind: SkiffConfig", "\"kind\":\"SkiffConfig\"", "\"kind\": \"SkiffConfig\""} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}
