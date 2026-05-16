package aws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestLowerServicePublicHTTPGoldenPrimitives(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
		ReleaseID:   "rel_01JABC",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}

	if lowered.Provider != "aws" || lowered.Service != "payments-api" || lowered.Env != "prod" {
		t.Fatalf("unexpected identity: %+v", lowered)
	}
	assertLen(t, "iam roles", lowered.IAMRoles, 1)
	assertLen(t, "instance profiles", lowered.InstanceProfiles, 1)
	assertLen(t, "security groups", lowered.SecurityGroups, 1)
	assertLen(t, "log groups", lowered.LogGroups, 1)
	assertLen(t, "metric configs", lowered.MetricConfigs, 1)
	assertLen(t, "target groups", lowered.TargetGroups, 1)
	assertLen(t, "listener rules", lowered.ListenerRules, 1)
	assertLen(t, "launch templates", lowered.LaunchTemplates, 1)
	assertLen(t, "auto scaling groups", lowered.AutoScalingGroups, 1)

	if lowered.TargetGroups[0].TargetType != "instance" || lowered.TargetGroups[0].Port != 8080 {
		t.Fatalf("target group should point at workload VM instances: %+v", lowered.TargetGroups[0])
	}
	if lowered.ListenerRules[0].Visibility != "public" || lowered.ListenerRules[0].Protocol != "HTTPS" || lowered.ListenerRules[0].CertificateRef == "" {
		t.Fatalf("public listener did not lower to HTTPS with certificate: %+v", lowered.ListenerRules[0])
	}
	sg := lowered.SecurityGroups[0]
	if len(sg.Ingress) != 1 || sg.Ingress[0].SourceSecurityGroupRef != "load-balancer" || sg.Ingress[0].CIDR != "" {
		t.Fatalf("instance ingress must come from the load balancer security group only: %+v", sg.Ingress)
	}
	if len(sg.Egress) != 1 || sg.Egress[0].CIDR != "0.0.0.0/0" {
		t.Fatalf("unexpected egress: %+v", sg.Egress)
	}
	userData := lowered.LaunchTemplates[0].UserData
	for _, want := range []string{
		`"state_bucket":"s3://skiff-state-prod"`,
		`"service":"payments-api"`,
		`"control_key":"services/payments-api/control.json"`,
		`"region":"us-west-2"`,
		`"release_id":"rel_01JABC"`,
		"skiff-runner",
	} {
		if !strings.Contains(userData, want) {
			t.Fatalf("launch template user-data missing %q:\n%s", want, userData)
		}
	}
	assertPolicyIsLeastPrivilege(t, lowered.IAMRoles[0])
}

func TestLowerServiceMinimalHasNoListenerRule(t *testing.T) {
	graph := compileSpec(t, minimalServiceSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-dev"})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	if len(lowered.ListenerRules) != 0 {
		t.Fatalf("private service should not create listener rules: %+v", lowered.ListenerRules)
	}
	if len(lowered.TargetGroups) != 1 || len(lowered.LaunchTemplates) != 1 || len(lowered.AutoScalingGroups) != 1 {
		t.Fatalf("minimal service missing core deployable resources: %+v", lowered)
	}
}

func TestLowerServiceInternalHTTP(t *testing.T) {
	graph := compileSpec(t, internalHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:                       "us-east-1",
		StateBucket:                  "s3://skiff-state-prod",
		LoadBalancerSecurityGroupRef: "sg-internal-alb",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	assertLen(t, "listener rules", lowered.ListenerRules, 1)
	listener := lowered.ListenerRules[0]
	if listener.Visibility != "internal" || listener.Protocol != "HTTP" || listener.Port != 80 || listener.CertificateRef != "" {
		t.Fatalf("unexpected internal listener: %+v", listener)
	}
	if got := lowered.SecurityGroups[0].Ingress[0].SourceSecurityGroupRef; got != "sg-internal-alb" {
		t.Fatalf("ingress source SG = %q, want sg-internal-alb", got)
	}
}

func TestProviderPlanUsesConcreteAWSLowering(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	p, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantKinds := map[string]bool{
		aws.ResourceKindIAMRole:            false,
		aws.ResourceKindIAMInstanceProfile: false,
		aws.ResourceKindSecurityGroup:      false,
		aws.ResourceKindLogGroup:           false,
		aws.ResourceKindMetricConfig:       false,
		aws.ResourceKindTargetGroup:        false,
		aws.ResourceKindListenerRule:       false,
		aws.ResourceKindLaunchTemplate:     false,
		aws.ResourceKindAutoScalingGroup:   false,
	}
	for _, resource := range plan.Resources {
		if _, ok := wantKinds[resource.Kind]; ok {
			wantKinds[resource.Kind] = true
		}
		if resource.Tags[ir.TagService] != "payments-api" || resource.Tags[ir.TagEnv] != "prod" {
			t.Fatalf("planned resource missing Skiff tags: %+v", resource)
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("plan missing kind %s: %+v", kind, plan.Resources)
		}
	}
}

func assertPolicyIsLeastPrivilege(t *testing.T, role aws.IAMRoleResource) {
	t.Helper()
	for _, stmt := range role.InlinePolicy.Statement {
		for _, resource := range stmt.Resource {
			if resource == "*" {
				t.Fatalf("policy statement %s uses wildcard resource: %+v", stmt.Sid, stmt)
			}
		}
	}
	var foundState, foundSecret, foundParameter bool
	for _, stmt := range role.InlinePolicy.Statement {
		if stmt.Sid == "ReadServiceState" {
			foundState = true
			if len(stmt.Resource) != 2 ||
				stmt.Resource[0] != "arn:aws:s3:::skiff-state-prod/services/payments-api/control.json" ||
				stmt.Resource[1] != "arn:aws:s3:::skiff-state-prod/services/payments-api/releases/*" {
				t.Fatalf("state policy is not scoped to service state: %+v", stmt)
			}
		}
		if stmt.Sid == "ReadReferencedSecretsManagerSecrets" {
			foundSecret = true
			if len(stmt.Action) != 1 || stmt.Action[0] != "secretsmanager:GetSecretValue" ||
				len(stmt.Resource) != 1 ||
				stmt.Resource[0] != "arn:aws:secretsmanager:us-west-2:123456789012:secret:payments/db-password" {
				t.Fatalf("secret policy should include exact Secrets Manager ARN: %+v", stmt)
			}
		}
		if stmt.Sid == "ReadReferencedSSMParameters" {
			foundParameter = true
			if len(stmt.Action) != 1 || stmt.Action[0] != "ssm:GetParameter" ||
				len(stmt.Resource) != 1 ||
				stmt.Resource[0] != "arn:aws:ssm:us-west-2:123456789012:parameter/payments/api-token" {
				t.Fatalf("parameter policy should include exact SSM parameter ARN: %+v", stmt)
			}
		}
	}
	if !foundState || !foundSecret || !foundParameter {
		t.Fatalf("missing least-privilege statements in role policy: %+v", role.InlinePolicy.Statement)
	}
}

func assertLen[T any](t *testing.T, name string, values []T, want int) {
	t.Helper()
	if len(values) != want {
		t.Fatalf("%s count = %d, want %d: %+v", name, len(values), want, values)
	}
}

func compileSpec(t *testing.T, body string) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(body), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile spec: %v", err)
	}
	return graph
}

const publicHTTPSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 3
  max: 20
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
secrets:
  - name: db-password
    ref: aws-secretsmanager://arn:aws:secretsmanager:us-west-2:123456789012:secret:payments/db-password
  - name: api-token
    ref: aws-ssm://arn:aws:ssm:us-west-2:123456789012:parameter/payments/api-token
`

const minimalServiceSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: worker-api
  env: dev
artifact:
  type: binary
  ref: file:///tmp/worker-api
runtime:
  port: 9000
  health:
    path: /healthz
`

const internalHTTPSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: orders-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/orders-api@sha256:def456
runtime:
  port: 8081
  health:
    path: /healthz
network:
  ingress:
    type: internal-http
    host: orders.internal.example.com
`
