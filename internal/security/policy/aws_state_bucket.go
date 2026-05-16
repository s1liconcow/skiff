package policy

import (
	"fmt"
	"sort"
	"strings"
)

type Role string

const (
	RoleStateBucket Role = "state-bucket"
	RoleRunner      Role = "runner"
	RoleDeployer    Role = "deployer"
	RoleSkiffd      Role = "skiffd"
	RoleBreakGlass  Role = "break-glass"
)

type Document struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

type Statement struct {
	Sid       string                       `json:"Sid"`
	Effect    string                       `json:"Effect"`
	Principal any                          `json:"Principal,omitempty"`
	Action    any                          `json:"Action"`
	Resource  any                          `json:"Resource"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

func StateBucketPolicy(bucket string) Document {
	bucketARN := bucketARN(bucket)
	objectARN := bucketARN + "/*"
	return Document{
		Version: "2012-10-17",
		Statement: []Statement{
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
				Sid:       "DenyMissingKMSEncryption",
				Effect:    "Deny",
				Principal: "*",
				Action:    "s3:PutObject",
				Resource:  objectARN,
				Condition: map[string]map[string]string{
					"Null": {"s3:x-amz-server-side-encryption": "true"},
				},
			},
			{
				Sid:       "DenyNonKMSEncryption",
				Effect:    "Deny",
				Principal: "*",
				Action:    "s3:PutObject",
				Resource:  objectARN,
				Condition: map[string]map[string]string{
					"StringNotEquals": {"s3:x-amz-server-side-encryption": "aws:kms"},
				},
			},
			{
				Sid:       "DenyStateDeletes",
				Effect:    "Deny",
				Principal: "*",
				Action:    []string{"s3:DeleteObject", "s3:DeleteObjectVersion"},
				Resource:  objectARN,
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
				},
			},
		},
	}
}

func PolicyForRole(role Role, bucket, kmsAlias string) (Document, error) {
	switch role {
	case RoleStateBucket:
		return StateBucketPolicy(bucket), nil
	case RoleRunner:
		return RunnerPolicy(bucket, kmsAlias), nil
	case RoleDeployer:
		return DeployerPolicy(bucket, kmsAlias), nil
	case RoleSkiffd:
		return SkiffdPolicy(bucket, kmsAlias), nil
	case RoleBreakGlass:
		return BreakGlassPolicy(bucket, kmsAlias), nil
	default:
		return Document{}, fmt.Errorf("unsupported policy role %q", role)
	}
}

func DeployerPolicy(bucket, kmsAlias string) Document {
	return Document{
		Version: "2012-10-17",
		Statement: compactStatements([]Statement{
			bucketLocationStatement(bucket),
			listPrefixStatement("ListServiceState", bucket, "services/*"),
			listPrefixStatement("ListSagaState", bucket, "sagas/*"),
			readOperatorStateStatement(bucket),
			createOnlyStateStatement("CreateImmutableState", bucket),
			casStateStatement("CASControlState", bucket),
			kmsStatement(kmsAlias, true),
		}),
	}
}

func RunnerPolicy(bucket, kmsAlias string) Document {
	return Document{
		Version: "2012-10-17",
		Statement: compactStatements([]Statement{
			bucketLocationStatement(bucket),
			listPrefixStatement("ListServiceState", bucket, "services/*"),
			{
				Sid:    "ReadServiceControlAndReleases",
				Effect: "Allow",
				Action: []string{"s3:GetObject"},
				Resource: []string{
					bucketARN(bucket) + "/envs/*/root.json",
					bucketARN(bucket) + "/services/*/control.json",
					bucketARN(bucket) + "/services/*/releases/*/release.json",
					bucketARN(bucket) + "/services/*/releases/*/runtime-manifest.json",
				},
			},
			kmsStatement(kmsAlias, false),
		}),
	}
}

func SkiffdPolicy(bucket, kmsAlias string) Document {
	statements := DeployerPolicy(bucket, kmsAlias).Statement
	statements = append(statements,
		listPrefixStatement("ListIndexState", bucket, "indexes/*"),
		listPrefixStatement("ListResourceState", bucket, "resources/*"),
		readSkiffdIndexAndResourceStatement(bucket),
	)
	return Document{Version: "2012-10-17", Statement: compactStatements(statements)}
}

func BreakGlassPolicy(bucket, kmsAlias string) Document {
	return Document{
		Version: "2012-10-17",
		Statement: compactStatements([]Statement{
			bucketLocationStatement(bucket),
			listPrefixStatement("ListAllStateForEmergency", bucket, "*"),
			{
				Sid:      "ReadAllStateForEmergency",
				Effect:   "Allow",
				Action:   []string{"s3:GetObject"},
				Resource: bucketARN(bucket) + "/*",
			},
			createOnlyStateStatement("EmergencyCreateOnlyState", bucket),
			casStateStatement("EmergencyCASControlState", bucket),
			kmsStatement(kmsAlias, true),
		}),
	}
}

func bucketLocationStatement(bucket string) Statement {
	return Statement{
		Sid:      "ReadStateBucketLocation",
		Effect:   "Allow",
		Action:   []string{"s3:GetBucketLocation"},
		Resource: bucketARN(bucket),
	}
}

func listPrefixStatement(sid, bucket, prefix string) Statement {
	return Statement{
		Sid:      sid,
		Effect:   "Allow",
		Action:   []string{"s3:ListBucket"},
		Resource: bucketARN(bucket),
		Condition: map[string]map[string]string{
			"StringLike": {"s3:prefix": prefix},
		},
	}
}

func readOperatorStateStatement(bucket string) Statement {
	return Statement{
		Sid:    "ReadOperationalState",
		Effect: "Allow",
		Action: []string{"s3:GetObject"},
		Resource: sortedStrings([]string{
			bucketARN(bucket) + "/envs/*/root.json",
			bucketARN(bucket) + "/services/*/control.json",
			bucketARN(bucket) + "/services/*/releases/*/release.json",
			bucketARN(bucket) + "/services/*/releases/*/runtime-manifest.json",
			bucketARN(bucket) + "/services/*/operations/*/intent.json",
			bucketARN(bucket) + "/services/*/operations/*/control.json",
			bucketARN(bucket) + "/services/*/operations/*/events/*",
			bucketARN(bucket) + "/sagas/*/intent.json",
			bucketARN(bucket) + "/sagas/*/graph.json",
			bucketARN(bucket) + "/sagas/*/control.json",
			bucketARN(bucket) + "/sagas/*/events/*",
			bucketARN(bucket) + "/audit/*/*",
		}),
	}
}

func readSkiffdIndexAndResourceStatement(bucket string) Statement {
	return Statement{
		Sid:    "ReadIndexesAndResources",
		Effect: "Allow",
		Action: []string{"s3:GetObject"},
		Resource: sortedStrings([]string{
			bucketARN(bucket) + "/indexes/*.json",
			bucketARN(bucket) + "/resources/by-logical/*/*",
			bucketARN(bucket) + "/resources/by-provider/*/*/*",
		}),
	}
}

func createOnlyStateStatement(sid, bucket string) Statement {
	return Statement{
		Sid:       sid,
		Effect:    "Allow",
		Action:    []string{"s3:PutObject"},
		Resource:  createOnlyResources(bucket),
		Condition: ifNoneMatchCondition(),
	}
}

func casStateStatement(sid, bucket string) Statement {
	return Statement{
		Sid:       sid,
		Effect:    "Allow",
		Action:    []string{"s3:PutObject"},
		Resource:  casResources(bucket),
		Condition: ifMatchCondition(),
	}
}

func kmsStatement(kmsAlias string, write bool) Statement {
	actions := []string{"kms:Decrypt", "kms:DescribeKey"}
	if write {
		actions = append(actions, "kms:Encrypt", "kms:GenerateDataKey")
	}
	return Statement{
		Sid:      "UseStateKMSKey",
		Effect:   "Allow",
		Action:   sortedStrings(actions),
		Resource: "*",
		Condition: map[string]map[string]string{
			"ForAnyValue:StringEquals": {"kms:ResourceAliases": kmsAlias},
		},
	}
}

func ifNoneMatchCondition() map[string]map[string]string {
	return map[string]map[string]string{
		"StringEquals": {"s3:if-none-match": "*"},
	}
}

func ifMatchCondition() map[string]map[string]string {
	return map[string]map[string]string{
		"Null": {"s3:if-match": "false"},
	}
}

func conditionalWriteResources(bucket string) []string {
	resources := append(createOnlyResources(bucket), casResources(bucket)...)
	resources = append(resources, indexResources(bucket)...)
	return sortedStrings(resources)
}

func createOnlyResources(bucket string) []string {
	return sortedStrings([]string{
		bucketARN(bucket) + "/envs/*/root.json",
		bucketARN(bucket) + "/services/*/releases/*/release.json",
		bucketARN(bucket) + "/services/*/releases/*/runtime-manifest.json",
		bucketARN(bucket) + "/services/*/operations/*/intent.json",
		bucketARN(bucket) + "/services/*/operations/*/events/*",
		bucketARN(bucket) + "/sagas/*/intent.json",
		bucketARN(bucket) + "/sagas/*/graph.json",
		bucketARN(bucket) + "/sagas/*/events/*",
		bucketARN(bucket) + "/audit/*/*",
		bucketARN(bucket) + "/resources/by-logical/*/*",
		bucketARN(bucket) + "/resources/by-provider/*/*/*",
	})
}

func casResources(bucket string) []string {
	return sortedStrings([]string{
		bucketARN(bucket) + "/services/*/control.json",
		bucketARN(bucket) + "/services/*/operations/*/control.json",
		bucketARN(bucket) + "/sagas/*/control.json",
	})
}

func indexResources(bucket string) []string {
	return sortedStrings([]string{
		bucketARN(bucket) + "/indexes/*.json",
	})
}

func bucketARN(bucket string) string {
	return "arn:aws:s3:::" + bucket
}

func compactStatements(statements []Statement) []Statement {
	out := make([]Statement, 0, len(statements))
	for _, statement := range statements {
		if strings.TrimSpace(statement.Sid) == "" {
			continue
		}
		out = append(out, statement)
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
