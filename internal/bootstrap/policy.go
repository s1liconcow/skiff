package bootstrap

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	securitypolicy "github.com/s1liconcow/skiff/internal/security/policy"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

type PolicyDocument = securitypolicy.Document
type PolicyStatement = securitypolicy.Statement

func StateBucketPolicy(bucket string) PolicyDocument {
	return securitypolicy.StateBucketPolicy(bucket)
}

func DeployerPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.DeployerPolicy(bucket, kmsAlias)
}

func RunnerPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.RunnerPolicy(bucket, kmsAlias)
}

func SkiffdPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.SkiffdPolicy(bucket, kmsAlias)
}

func BreakGlassPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.BreakGlassPolicy(bucket, kmsAlias)
}

func PolicyJSON(policy PolicyDocument) (string, error) {
	body, err := canonical.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func TerraformAWS(plan *AWSPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("aws bootstrap plan is required")
	}
	bucketPolicy, err := PolicyJSON(plan.BucketPolicy)
	if err != nil {
		return "", err
	}
	var policyVars []terraformPolicy
	for _, name := range sortedPolicyNames(plan.IAMPolicies) {
		body, err := PolicyJSON(plan.IAMPolicies[name])
		if err != nil {
			return "", err
		}
		policyVars = append(policyVars, terraformPolicy{Name: name, JSON: body})
	}
	data := struct {
		Plan         *AWSPlan
		BucketPolicy string
		Policies     []terraformPolicy
	}{
		Plan:         plan,
		BucketPolicy: bucketPolicy,
		Policies:     policyVars,
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.New("terraform").Parse(terraformTemplate))
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}

type terraformPolicy struct {
	Name string
	JSON string
}

const terraformTemplate = `
resource "aws_kms_key" "skiff_state" {
  description         = "Skiff state encryption key for {{ .Plan.Env }}"
  enable_key_rotation = true
}

resource "aws_kms_alias" "skiff_state" {
  name          = "{{ .Plan.KMSAlias }}"
  target_key_id = aws_kms_key.skiff_state.key_id
}

resource "aws_s3_bucket" "skiff_state" {
  bucket = "{{ .Plan.StateBucket }}"
}

resource "aws_s3_bucket_versioning" "skiff_state" {
  bucket = aws_s3_bucket.skiff_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "skiff_state" {
  bucket                  = aws_s3_bucket.skiff_state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "skiff_state" {
  bucket = aws_s3_bucket.skiff_state.id
  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_alias.skiff_state.arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_policy" "skiff_state" {
  bucket = aws_s3_bucket.skiff_state.id
  policy = <<POLICY
{{ .BucketPolicy }}
POLICY
}
{{ range .Policies }}
resource "aws_iam_role" "skiff_{{ .Name }}" {
  name = "{{ index $.Plan.RootConfig.Roles .Name }}"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { AWS = "*" }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "skiff_{{ .Name }}" {
  name   = "skiff-{{ $.Plan.Env }}-{{ .Name }}"
  role   = aws_iam_role.skiff_{{ .Name }}.id
  policy = <<POLICY
{{ .JSON }}
POLICY
}
{{ end }}`
