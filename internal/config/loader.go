package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type LoadOptions struct {
	ModeDefault  Mode
	ConfigPath   string
	Context      string
	UserDataPath string
	Env          map[string]string
	Overrides    map[string]string
}

func Load(opts LoadOptions) (Loaded, error) {
	env := opts.Env
	if env == nil {
		env = environ()
	}

	configPath, contextName := ResolveConfigSelection(opts.ConfigPath, opts.Context, env)

	loaded := Defaults(opts.ModeDefault)
	if configPath != "" {
		loaded.ConfigPath = configPath
		values, selectedContext, err := parseConfigFile(configPath, contextName)
		if err != nil {
			return Loaded{}, err
		}
		source := "file:" + configPath
		if selectedContext != "" {
			source += "#context:" + selectedContext
			loaded.Context = selectedContext
		}
		applyValues(&loaded, values, source)
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
		FieldEnv:                             cfg.Env,
		FieldEnvironmentClass:                cfg.EnvironmentClass,
		FieldProvider:                        cfg.Provider,
		FieldRegion:                          cfg.Region,
		FieldStateBucket:                     cfg.StateBucket,
		FieldKMSKey:                          cfg.KMSKey,
		FieldAuthMode:                        cfg.AuthMode,
		FieldLogLevel:                        cfg.LogLevel,
		FieldMode:                            string(cfg.Mode),
		FieldAPIURL:                          cfg.APIURL,
		FieldService:                         cfg.Service,
		FieldControlKey:                      cfg.ControlKey,
		FieldReleaseID:                       cfg.ReleaseID,
		FieldReleaseManifestKey:              cfg.ReleaseManifestKey,
		FieldRuntimeManifestKey:              cfg.RuntimeManifestKey,
		FieldReleaseSigningKeyID:             cfg.ReleaseSigningKeyID,
		FieldReleaseSigningKeyRef:            cfg.ReleaseSigningKeyRef,
		FieldWriteRoleARN:                    cfg.WriteRoleARN,
		FieldAWSLiveApply:                    boolConfigValue(cfg.AWSLiveApply),
		FieldAWSVPCID:                        cfg.AWSVPCID,
		FieldAWSSubnetIDs:                    strings.Join(cfg.AWSSubnetIDs, ","),
		FieldAWSAMIID:                        cfg.AWSAMIID,
		FieldAWSALBListenerARN:               cfg.AWSALBListenerARN,
		FieldAWSLoadBalancerSecurityGroupRef: cfg.AWSLoadBalancerSecurityGroupRef,
	}
	if cfg.ReleasePolicy != nil {
		values[FieldRequireSignedReleases] = strconv.FormatBool(cfg.ReleasePolicy.RequireSignedReleases)
		values[FieldAllowUnsignedCode] = strconv.FormatBool(cfg.ReleasePolicy.AllowUnsignedCode)
	}
	if cfg.StatefulGroup != "" {
		values[FieldStatefulGroup] = cfg.StatefulGroup
		values[FieldStatefulMember] = strconv.Itoa(cfg.StatefulMember)
		values[FieldStatefulGeneration] = strconv.FormatInt(cfg.StatefulGeneration, 10)
		values[FieldStatefulVolumeMountPath] = cfg.StatefulVolumeMountPath
		values[FieldStatefulStableHostname] = cfg.StatefulStableHostname
		values[FieldStatefulRecipe] = cfg.StatefulRecipe
	}
	applyValues(loaded, values, source)
	if cfg.Logs != nil {
		logs := *cfg.Logs
		logs.Labels = cloneStringMap(logs.Labels)
		loaded.Config.Logs = &logs
		loaded.Sources[FieldLogs] = source
	}
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
		case FieldEnvironmentClass:
			loaded.Config.EnvironmentClass = strings.TrimSpace(value)
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
		case FieldReleaseID:
			loaded.Config.ReleaseID = value
		case FieldStatefulGroup:
			loaded.Config.StatefulGroup = strings.TrimSpace(value)
		case FieldStatefulMember:
			parsed, err := strconv.Atoi(value)
			if err == nil {
				loaded.Config.StatefulMember = parsed
			}
		case FieldStatefulGeneration:
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				loaded.Config.StatefulGeneration = parsed
			}
		case FieldStatefulVolumeMountPath:
			loaded.Config.StatefulVolumeMountPath = strings.TrimSpace(value)
		case FieldStatefulStableHostname:
			loaded.Config.StatefulStableHostname = strings.TrimSpace(value)
		case FieldStatefulRecipe:
			loaded.Config.StatefulRecipe = strings.TrimSpace(value)
		case FieldReleaseManifestKey:
			loaded.Config.ReleaseManifestKey = strings.TrimSpace(value)
		case FieldRuntimeManifestKey:
			loaded.Config.RuntimeManifestKey = strings.TrimSpace(value)
		case FieldReleaseSigningKeyID:
			loaded.Config.ReleaseSigningKeyID = strings.TrimSpace(value)
		case FieldReleaseSigningKeyRef:
			loaded.Config.ReleaseSigningKeyRef = strings.TrimSpace(value)
		case FieldRequireSignedReleases:
			parsed, err := strconv.ParseBool(value)
			if err == nil {
				if loaded.Config.ReleasePolicy == nil {
					loaded.Config.ReleasePolicy = &ReleasePolicy{}
				}
				loaded.Config.ReleasePolicy.RequireSignedReleases = parsed
			}
		case FieldAllowUnsignedCode:
			parsed, err := strconv.ParseBool(value)
			if err == nil {
				if loaded.Config.ReleasePolicy == nil {
					loaded.Config.ReleasePolicy = &ReleasePolicy{}
				}
				loaded.Config.ReleasePolicy.AllowUnsignedCode = parsed
			}
		case FieldWriteRoleARN:
			loaded.Config.WriteRoleARN = strings.TrimSpace(value)
		case FieldAWSLiveApply:
			parsed, err := strconv.ParseBool(value)
			if err == nil {
				loaded.Config.AWSLiveApply = parsed
			}
		case FieldAWSVPCID:
			loaded.Config.AWSVPCID = strings.TrimSpace(value)
		case FieldAWSSubnetIDs:
			loaded.Config.AWSSubnetIDs = splitCommaValues(value)
		case FieldAWSAMIID:
			loaded.Config.AWSAMIID = strings.TrimSpace(value)
		case FieldAWSALBListenerARN:
			loaded.Config.AWSALBListenerARN = strings.TrimSpace(value)
		case FieldAWSLoadBalancerSecurityGroupRef:
			loaded.Config.AWSLoadBalancerSecurityGroupRef = strings.TrimSpace(value)
		default:
			continue
		}
		loaded.Sources[field] = source
	}
}

func parseConfigFile(path, contextName string) (map[string]string, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read config %q: %w", path, err)
	}
	if looksLikeSkiffConfig(body) {
		file, err := ParseSkiffConfigFile(body, "config "+path)
		if err != nil {
			return nil, "", err
		}
		selected, values, err := file.SelectContext(contextName)
		if err != nil {
			return nil, "", fmt.Errorf("config %q: %w", path, err)
		}
		return values, selected, nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		values, err := parseJSONMap(body, "config "+path)
		return values, "", err
	case ".yaml", ".yml", "":
		values, err := parseFlatYAML(body, "config "+path)
		return values, "", err
	default:
		return nil, "", fmt.Errorf("config %q: unsupported extension; expected .json, .yaml, or .yml", path)
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
	case "environment_class", "environmentClass":
		return FieldEnvironmentClass, nil
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
	case "release_id", "releaseID", "releaseId":
		return FieldReleaseID, nil
	case "release_manifest_key", "releaseManifestKey":
		return FieldReleaseManifestKey, nil
	case "runtime_manifest_key", "runtimeManifestKey":
		return FieldRuntimeManifestKey, nil
	case "release_signing_key_id", "releaseSigningKeyID", "releaseSigningKeyId":
		return FieldReleaseSigningKeyID, nil
	case "release_signing_key_ref", "releaseSigningKeyRef":
		return FieldReleaseSigningKeyRef, nil
	case "require_signed_releases", "requireSignedReleases":
		return FieldRequireSignedReleases, nil
	case "allow_unsigned_code", "allowUnsignedCode":
		return FieldAllowUnsignedCode, nil
	case "write_role_arn", "writeRoleARN", "writeRoleArn":
		return FieldWriteRoleARN, nil
	case "stateful_group", "statefulGroup":
		return FieldStatefulGroup, nil
	case "stateful_member", "statefulMember":
		return FieldStatefulMember, nil
	case "stateful_generation", "statefulGeneration":
		return FieldStatefulGeneration, nil
	case "stateful_volume_mount_path", "statefulVolumeMountPath":
		return FieldStatefulVolumeMountPath, nil
	case "stateful_stable_hostname", "statefulStableHostname":
		return FieldStatefulStableHostname, nil
	case "stateful_recipe", "statefulRecipe":
		return FieldStatefulRecipe, nil
	case "aws_live_apply", "awsLiveApply":
		return FieldAWSLiveApply, nil
	case "aws_vpc_id", "awsVPCID", "awsVpcID", "awsVpcId":
		return FieldAWSVPCID, nil
	case "aws_subnet_ids", "awsSubnetIDs", "awsSubnetIds":
		return FieldAWSSubnetIDs, nil
	case "aws_ami_id", "awsAMIID", "awsAmiID", "awsAmiId":
		return FieldAWSAMIID, nil
	case "aws_alb_listener_arn", "awsALBListenerARN", "awsAlbListenerARN", "awsAlbListenerArn":
		return FieldAWSALBListenerARN, nil
	case "aws_load_balancer_security_group_ref", "awsLoadBalancerSecurityGroupRef":
		return FieldAWSLoadBalancerSecurityGroupRef, nil
	default:
		return "", fmt.Errorf("unknown field %q", key)
	}
}

func valuesFromEnv(env map[string]string) map[string]string {
	return map[string]string{
		FieldEnv:                             env["SKIFF_ENV"],
		FieldEnvironmentClass:                env["SKIFF_ENVIRONMENT_CLASS"],
		FieldProvider:                        env["SKIFF_PROVIDER"],
		FieldRegion:                          env["SKIFF_REGION"],
		FieldStateBucket:                     env["SKIFF_STATE_BUCKET"],
		FieldKMSKey:                          env["SKIFF_KMS_KEY"],
		FieldAuthMode:                        env["SKIFF_AUTH_MODE"],
		FieldLogLevel:                        env["SKIFF_LOG_LEVEL"],
		FieldMode:                            env["SKIFF_MODE"],
		FieldAPIURL:                          env["SKIFF_API_URL"],
		FieldService:                         env["SKIFF_SERVICE"],
		FieldControlKey:                      env["SKIFF_CONTROL_KEY"],
		FieldReleaseID:                       env["SKIFF_RELEASE_ID"],
		FieldReleaseManifestKey:              env["SKIFF_RELEASE_MANIFEST_KEY"],
		FieldRuntimeManifestKey:              env["SKIFF_RUNTIME_MANIFEST_KEY"],
		FieldReleaseSigningKeyID:             env["SKIFF_RELEASE_SIGNING_KEY_ID"],
		FieldReleaseSigningKeyRef:            env["SKIFF_RELEASE_SIGNING_KEY_REF"],
		FieldRequireSignedReleases:           env["SKIFF_REQUIRE_SIGNED_RELEASES"],
		FieldAllowUnsignedCode:               env["SKIFF_ALLOW_UNSIGNED_CODE"],
		FieldWriteRoleARN:                    firstNonEmptyEnv(env, "SKIFF_WRITE_ROLE_ARN", "SKIFF_DEPLOYER_ROLE_ARN"),
		FieldStatefulGroup:                   env["SKIFF_STATEFUL_GROUP"],
		FieldStatefulMember:                  env["SKIFF_STATEFUL_MEMBER"],
		FieldStatefulGeneration:              env["SKIFF_STATEFUL_GENERATION"],
		FieldStatefulVolumeMountPath:         env["SKIFF_STATEFUL_VOLUME_MOUNT_PATH"],
		FieldStatefulStableHostname:          env["SKIFF_STATEFUL_STABLE_HOSTNAME"],
		FieldStatefulRecipe:                  env["SKIFF_STATEFUL_RECIPE"],
		FieldAWSLiveApply:                    env["SKIFF_AWS_LIVE_APPLY"],
		FieldAWSVPCID:                        env["SKIFF_AWS_VPC_ID"],
		FieldAWSSubnetIDs:                    env["SKIFF_AWS_SUBNET_IDS"],
		FieldAWSAMIID:                        env["SKIFF_AWS_AMI_ID"],
		FieldAWSALBListenerARN:               env["SKIFF_AWS_ALB_LISTENER_ARN"],
		FieldAWSLoadBalancerSecurityGroupRef: env["SKIFF_AWS_LOAD_BALANCER_SECURITY_GROUP_REF"],
	}
}

func boolConfigValue(value bool) string {
	if value {
		return "true"
	}
	return ""
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmptyEnv(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	return ""
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
