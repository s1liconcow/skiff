package bootstrap

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/s1liconcow/skiff/internal/state/canonical"
)

type PolicyDocument struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

type PolicyStatement struct {
	Sid       string                       `json:"Sid"`
	Effect    string                       `json:"Effect"`
	Principal any                          `json:"Principal,omitempty"`
	Action    any                          `json:"Action"`
	Resource  any                          `json:"Resource"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

func StateBucketPolicy(bucket string) PolicyDocument {
	bucketARN := "arn:aws:s3:::" + bucket
	objectARN := bucketARN + "/*"
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:       "DenyInsecureTransport",
				Effect:    "Deny",
				Principal: "*",
				Action:    "s3:*",
				Resource:  []string{bucketARN, objectARN},
				Condition: map[string]map[string]string{
					"Bool": {"aws:SecureTransport": "false"},
				},
			},
			{
				Sid:       "DenyUnconditionalStateWrites",
				Effect:    "Deny",
				Principal: "*",
				Action:    "s3:PutObject",
				Resource:  conditionalWriteResources(bucket),
				Condition: map[string]map[string]string{
					"Null": {
						"s3:if-match":      "true",
						"s3:if-none-match": "true",
					},
					"Bool": {
						"s3:ObjectCreationOperation": "true",
					},
				},
			},
		},
	}
}

func DeployerPolicy(bucket, kmsAlias string) PolicyDocument {
	bucketARN := "arn:aws:s3:::" + bucket
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:      "ListSkiffState",
				Effect:   "Allow",
				Action:   []string{"s3:ListBucket", "s3:GetBucketLocation"},
				Resource: bucketARN,
			},
			{
				Sid:      "ReadWriteSkiffState",
				Effect:   "Allow",
				Action:   []string{"s3:GetObject", "s3:PutObject"},
				Resource: bucketARN + "/*",
			},
			kmsPolicyStatement(kmsAlias, true),
		},
	}
}

func RunnerPolicy(bucket, kmsAlias string) PolicyDocument {
	bucketARN := "arn:aws:s3:::" + bucket
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:      "ListServiceState",
				Effect:   "Allow",
				Action:   []string{"s3:ListBucket", "s3:GetBucketLocation"},
				Resource: bucketARN,
				Condition: map[string]map[string]string{
					"StringLike": {"s3:prefix": "services/*"},
				},
			},
			{
				Sid:    "ReadServiceControlAndReleases",
				Effect: "Allow",
				Action: []string{"s3:GetObject"},
				Resource: []string{
					bucketARN + "/envs/*/root.json",
					bucketARN + "/services/*/control.json",
					bucketARN + "/services/*/releases/*/release.json",
					bucketARN + "/services/*/releases/*/runtime-manifest.json",
				},
			},
			{
				Sid:    "WriteRunnerObservationsAndEvents",
				Effect: "Allow",
				Action: []string{"s3:PutObject"},
				Resource: []string{
					bucketARN + "/observations/services/*/*",
					bucketARN + "/services/*/operations/*/events/*",
				},
			},
			kmsPolicyStatement(kmsAlias, false),
		},
	}
}

func SkiffdPolicy(bucket, kmsAlias string) PolicyDocument {
	policy := DeployerPolicy(bucket, kmsAlias)
	policy.Statement[1].Sid = "ReadWriteSkiffStateForSkiffd"
	return policy
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

func conditionalWriteResources(bucket string) []string {
	bucketARN := "arn:aws:s3:::" + bucket
	resources := []string{
		bucketARN + "/envs/*/root.json",
		bucketARN + "/services/*/control.json",
		bucketARN + "/services/*/releases/*/release.json",
		bucketARN + "/services/*/releases/*/runtime-manifest.json",
		bucketARN + "/services/*/operations/*/intent.json",
		bucketARN + "/services/*/operations/*/control.json",
		bucketARN + "/services/*/operations/*/events/*",
		bucketARN + "/sagas/*/intent.json",
		bucketARN + "/sagas/*/graph.json",
		bucketARN + "/sagas/*/control.json",
		bucketARN + "/sagas/*/events/*",
		bucketARN + "/audit/*/*",
		bucketARN + "/resources/by-logical/*/*",
		bucketARN + "/resources/by-provider/*/*/*",
	}
	sort.Strings(resources)
	return resources
}

func kmsPolicyStatement(kmsAlias string, write bool) PolicyStatement {
	actions := []string{"kms:Decrypt", "kms:DescribeKey"}
	if write {
		actions = append(actions, "kms:Encrypt", "kms:GenerateDataKey")
	} else {
		actions = append(actions, "kms:Encrypt", "kms:GenerateDataKey")
	}
	sort.Strings(actions)
	return PolicyStatement{
		Sid:      "UseStateKMSKey",
		Effect:   "Allow",
		Action:   actions,
		Resource: "*",
		Condition: map[string]map[string]string{
			"ForAnyValue:StringEquals": {"kms:ResourceAliases": kmsAlias},
		},
	}
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
