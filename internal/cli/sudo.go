package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	"github.com/s1liconcow/skiff/internal/events"
	awsprovider "github.com/s1liconcow/skiff/internal/provider/aws"
)

type sudoAssumeRequest struct {
	RoleARN               string
	RoleSessionName       string
	SourceIdentity        string
	TraceID               string
	BusinessJustification string
	Region                string
	DurationSeconds       int32
}

type sudoCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
	AssumedRoleARN  string
	SourceIdentity  string
}

type sudoOutput struct {
	OK                    bool              `json:"ok"`
	TraceID               string            `json:"trace_id"`
	RoleARN               string            `json:"role_arn"`
	AssumedRoleARN        string            `json:"assumed_role_arn,omitempty"`
	SourceIdentity        string            `json:"source_identity"`
	BusinessJustification string            `json:"business_justification"`
	ExpiresAt             string            `json:"expires_at,omitempty"`
	Exports               map[string]string `json:"exports"`
	EvalCommand           string            `json:"eval_command"`
}

var assumeSudoRole = func(ctx context.Context, req sudoAssumeRequest) (*sudoCredentials, error) {
	creds, err := awsprovider.AssumeWriteRole(ctx, awsprovider.Config{Region: req.Region}, awsprovider.AssumeWriteRoleOptions{
		RoleARN:               req.RoleARN,
		RoleSessionName:       req.RoleSessionName,
		SourceIdentity:        req.SourceIdentity,
		TraceID:               req.TraceID,
		BusinessJustification: req.BusinessJustification,
		DurationSeconds:       req.DurationSeconds,
	})
	if err != nil {
		return nil, err
	}
	return &sudoCredentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expiration:      creds.Expiration,
		AssumedRoleARN:  creds.AssumedRoleARN,
		SourceIdentity:  creds.SourceIdentity,
	}, nil
}

func runSudo(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" sudo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: shell, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier for subsequent Skiff commands")
	roleARN := fs.String("role-arn", "", "write IAM role ARN; defaults to config writeRoleARN or SKIFF_WRITE_ROLE_ARN")
	sourceIdentity := fs.String("source-identity", defaultSourceIdentity(), "STS source identity for CloudTrail attribution")
	sessionName := fs.String("session-name", "", "STS role session name")
	duration := fs.Duration("duration", time.Hour, "temporary credential duration")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")

	justification := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		justification = strings.TrimSpace(args[0])
		args = append([]string(nil), args[1:]...)
	}
	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeSudoError(binary, *format, *traceID, err, stdout, stderr)
	}
	if justification == "" && fs.NArg() == 1 {
		justification = strings.TrimSpace(fs.Arg(0))
	} else if justification != "" && fs.NArg() != 0 {
		return writeSudoError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	if justification == "" {
		return writeSudoError(binary, *format, *traceID, errors.New("business justification is required, for example: skiff sudo JIRA-1234"), stdout, stderr)
	}
	if err := validateSudoJustification(justification); err != nil {
		return writeSudoError(binary, *format, *traceID, err, stdout, stderr)
	}
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: config.ModeDirect,
		ConfigPath:  *configPath,
		Context:     *contextName,
		Overrides:   root.configOverrides(),
	})
	if err != nil {
		return writeSudoError(binary, *format, *traceID, err, stdout, stderr)
	}
	resolvedRoleARN := firstNonEmptyString(*roleARN, loaded.Config.WriteRoleARN)
	if resolvedRoleARN == "" {
		return writeSudoError(binary, *format, *traceID, errors.New("write role ARN is required; set --role-arn, writeRoleARN in .skiffconfig, or SKIFF_WRITE_ROLE_ARN"), stdout, stderr)
	}
	resolvedTraceID := strings.TrimSpace(*traceID)
	if resolvedTraceID == "" {
		resolvedTraceID = "tr_" + events.NewID(time.Now().UTC(), resolvedRoleARN+"\x00"+justification)
	}
	resolvedSource := strings.TrimSpace(*sourceIdentity)
	if err := validateSudoSourceIdentity(resolvedSource); err != nil {
		return writeSudoError(binary, *format, resolvedTraceID, err, stdout, stderr)
	}
	durationSeconds, err := sudoDurationSeconds(*duration)
	if err != nil {
		return writeSudoError(binary, *format, resolvedTraceID, err, stdout, stderr)
	}
	resolvedSessionName := strings.TrimSpace(*sessionName)
	if resolvedSessionName == "" {
		resolvedSessionName = defaultSudoSessionName(resolvedSource, resolvedTraceID)
	}

	creds, err := assumeSudoRole(context.Background(), sudoAssumeRequest{
		RoleARN:               resolvedRoleARN,
		RoleSessionName:       resolvedSessionName,
		SourceIdentity:        resolvedSource,
		TraceID:               resolvedTraceID,
		BusinessJustification: justification,
		Region:                firstNonEmptyString(loaded.Config.Region, root.Region),
		DurationSeconds:       durationSeconds,
	})
	if err != nil {
		return writeSudoError(binary, *format, resolvedTraceID, err, stdout, stderr)
	}
	exports := sudoExports(resolvedTraceID, resolvedRoleARN, resolvedSource, justification, creds)
	out := sudoOutput{
		OK:                    true,
		TraceID:               resolvedTraceID,
		RoleARN:               resolvedRoleARN,
		AssumedRoleARN:        creds.AssumedRoleARN,
		SourceIdentity:        resolvedSource,
		BusinessJustification: justification,
		Exports:               exports,
		EvalCommand:           fmt.Sprintf(`eval "$(%s sudo %s)"`, binary, shellQuote(justification)),
	}
	if !creds.Expiration.IsZero() {
		out.ExpiresAt = creds.Expiration.UTC().Format(time.RFC3339)
	}

	switch {
	case *format == "human" || *format == "text" || *format == "shell" || *format == "":
		writeShellExports(stdout, exports)
		return ExitSuccess
	case isJSONFormat(*format):
		if err := writeJSON(stdout, *format, out); err != nil {
			fmt.Fprintf(stderr, "%s sudo: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeSudoError(binary, *format, resolvedTraceID, errors.New(`unsupported format; expected "shell", "json", or "json-pretty"`), stdout, stderr)
	}
}

func sudoExports(traceID, roleARN, sourceIdentity, justification string, creds *sudoCredentials) map[string]string {
	exports := map[string]string{
		"AWS_ACCESS_KEY_ID":              creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY":          creds.SecretAccessKey,
		"AWS_SESSION_TOKEN":              creds.SessionToken,
		"SKIFF_TRACE_ID":                 traceID,
		"SKIFF_WRITE_ROLE_ARN":           roleARN,
		"SKIFF_SOURCE_IDENTITY":          sourceIdentity,
		"SKIFF_BUSINESS_JUSTIFICATION":   justification,
		"SKIFF_SUDO_ASSUMED_ROLE_ARN":    creds.AssumedRoleARN,
		"SKIFF_SUDO_ROLE_EXPIRES_AT_UTC": "",
	}
	if !creds.Expiration.IsZero() {
		exports["SKIFF_SUDO_ROLE_EXPIRES_AT_UTC"] = creds.Expiration.UTC().Format(time.RFC3339)
	}
	return exports
}

func writeShellExports(w io.Writer, exports map[string]string) {
	keys := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"SKIFF_TRACE_ID",
		"SKIFF_WRITE_ROLE_ARN",
		"SKIFF_SOURCE_IDENTITY",
		"SKIFF_BUSINESS_JUSTIFICATION",
		"SKIFF_SUDO_ASSUMED_ROLE_ARN",
		"SKIFF_SUDO_ROLE_EXPIRES_AT_UTC",
	}
	for _, key := range keys {
		if value, ok := exports[key]; ok && value != "" {
			fmt.Fprintf(w, "export %s=%s\n", key, shellQuote(value))
		}
	}
}

func validateSudoJustification(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("business justification is required")
	}
	if len(value) > 256 {
		return errors.New("business justification must be 256 bytes or fewer for STS session tags")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("business justification must be a single line")
	}
	return nil
}

func validateSudoSourceIdentity(value string) error {
	if value == "" {
		return errors.New("source identity is required; set --source-identity or SKIFF_SOURCE_IDENTITY")
	}
	if strings.HasPrefix(strings.ToLower(value), "aws:") {
		return errors.New("source identity must not start with aws:")
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("+=,.@-_", r) {
			continue
		}
		return fmt.Errorf("source identity contains unsupported character %q", r)
	}
	return nil
}

func sudoDurationSeconds(value time.Duration) (int32, error) {
	if value < 15*time.Minute {
		return 0, errors.New("duration must be at least 15m")
	}
	if value > time.Hour {
		return 0, errors.New("duration must be at most 1h for Skiff write escalation")
	}
	return int32(value / time.Second), nil
}

func defaultSourceIdentity() string {
	if value := strings.TrimSpace(os.Getenv("SKIFF_SOURCE_IDENTITY")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("USERNAME")); value != "" {
		return value
	}
	return ""
}

func defaultSudoSessionName(sourceIdentity, traceID string) string {
	base := "skiff-" + sanitizeRoleSessionPart(sourceIdentity)
	suffix := sanitizeRoleSessionPart(traceID)
	if len(suffix) > 20 {
		suffix = suffix[len(suffix)-20:]
	}
	name := base + "-" + suffix
	if len(name) > 64 {
		name = name[:64]
	}
	return strings.Trim(name, "-")
}

func sanitizeRoleSessionPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("+=,.@-_", r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "operator"
	}
	return out
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeSudoError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "assume_write_role",
					Command:  binary + " sudo JIRA-1234 --role-arn <role-arn>",
					Mutating: false,
				},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s sudo: %v\n", binary, err)
	return ExitUserError
}
