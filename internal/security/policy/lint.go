package policy

import "strings"

type Finding struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Statement string `json:"statement,omitempty"`
	Summary   string `json:"summary"`
}

type LintOptions struct {
	Role Role
}

func Lint(document Document, opts LintOptions) []Finding {
	var findings []Finding
	hasTLSDeny := false
	hasEncryptionDeny := false

	for _, statement := range document.Statement {
		actions := stringValues(statement.Action)
		resources := stringValues(statement.Resource)
		if isTLSDeny(statement) {
			hasTLSDeny = true
		}
		if isKMSEncryptionDeny(statement) {
			hasEncryptionDeny = true
		}
		if containsAction(actions, "s3:*") && !allowWildcardS3Action(statement) {
			findings = append(findings, finding("DANGEROUS_WILDCARD_ACTION", statement.Sid, "s3:* is only allowed in explicit deny guardrails"))
		}
		if containsResource(resources, "*") && !allowWildcardResource(statement) {
			findings = append(findings, finding("DANGEROUS_WILDCARD_RESOURCE", statement.Sid, "Resource * is not allowed for state policies"))
		}
		if statement.Effect == "Allow" && hasActionPrefix(actions, "s3:Delete") {
			findings = append(findings, finding("DELETE_STATE_PRIVILEGE", statement.Sid, "default state roles must not grant S3 delete privileges"))
		}
		if statement.Effect == "Allow" && containsAction(actions, "s3:PutObject") && !hasConditionalWrite(statement) {
			findings = append(findings, finding("UNCONDITIONED_STATE_WRITE", statement.Sid, "state writes must require If-Match or If-None-Match conditions"))
		}
	}

	if opts.Role == RoleStateBucket {
		if !hasTLSDeny {
			findings = append(findings, finding("MISSING_TLS_DENY", "", "state bucket policy must deny insecure transport"))
		}
		if !hasEncryptionDeny {
			findings = append(findings, finding("MISSING_KMS_ENCRYPTION_DENY", "", "state bucket policy must require aws:kms server-side encryption"))
		}
	}
	return findings
}

func finding(code, statement, summary string) Finding {
	return Finding{Code: code, Severity: "error", Statement: statement, Summary: summary}
}

func allowWildcardS3Action(statement Statement) bool {
	return statement.Effect == "Deny" && isTLSDeny(statement)
}

func allowWildcardResource(statement Statement) bool {
	actions := stringValues(statement.Action)
	return hasActionPrefix(actions, "kms:") && hasKMSAliasCondition(statement)
}

func isTLSDeny(statement Statement) bool {
	if statement.Effect != "Deny" {
		return false
	}
	boolConditions := statement.Condition["Bool"]
	return boolConditions["aws:SecureTransport"] == "false"
}

func isKMSEncryptionDeny(statement Statement) bool {
	if statement.Effect != "Deny" {
		return false
	}
	for _, values := range statement.Condition {
		if _, ok := values["s3:x-amz-server-side-encryption"]; ok {
			return true
		}
	}
	return false
}

func hasConditionalWrite(statement Statement) bool {
	for _, values := range statement.Condition {
		for key, value := range values {
			switch strings.ToLower(key) {
			case "s3:if-match":
				if value != "" && value != "true" {
					return true
				}
			case "s3:if-none-match":
				if value != "" && value != "true" {
					return true
				}
			}
		}
	}
	return false
}

func hasKMSAliasCondition(statement Statement) bool {
	for _, values := range statement.Condition {
		if values["kms:ResourceAliases"] != "" {
			return true
		}
	}
	return false
}

func containsAction(actions []string, want string) bool {
	want = strings.ToLower(want)
	for _, action := range actions {
		if strings.ToLower(action) == want {
			return true
		}
	}
	return false
}

func hasActionPrefix(actions []string, prefix string) bool {
	prefix = strings.ToLower(prefix)
	for _, action := range actions {
		if strings.HasPrefix(strings.ToLower(action), prefix) {
			return true
		}
	}
	return false
}

func containsResource(resources []string, want string) bool {
	for _, resource := range resources {
		if resource == want {
			return true
		}
	}
	return false
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
