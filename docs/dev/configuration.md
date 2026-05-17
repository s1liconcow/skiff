# Configuration

Skiff loads runtime configuration from four sources, with later sources taking
precedence:

1. compiled defaults
2. config file
3. environment variables
4. command-line flags

Use `skiff config show` to inspect the effective values and their sources:

```bash
skiff config show --mode direct --config ./skiff.yaml --format json
```

Supported file formats are strict flat JSON and flat YAML. Unknown fields are
rejected so operators do not accidentally believe unsupported configuration is
active.

```yaml
env: prod
provider: aws
region: us-west-2
stateBucket: s3://skiff-state-prod
kmsKey: alias/skiff-prod
mode: direct
authMode: none
logLevel: info
apiURL: https://skiff.example.com
```

Environment variable equivalents:

```text
SKIFF_CONFIG
SKIFF_ENV
SKIFF_PROVIDER
SKIFF_REGION
SKIFF_STATE_BUCKET
SKIFF_KMS_KEY
SKIFF_AUTH_MODE
SKIFF_LOG_LEVEL
SKIFF_MODE
SKIFF_API_URL
SKIFF_SERVICE
SKIFF_CONTROL_KEY
SKIFF_AWS_LIVE_APPLY
SKIFF_AWS_VPC_ID
SKIFF_AWS_SUBNET_IDS
SKIFF_AWS_AMI_ID
SKIFF_AWS_ALB_LISTENER_ARN
SKIFF_AWS_LOAD_BALANCER_SECURITY_GROUP_REF
```

Mode-specific required fields:

| Mode | Required fields |
| --- | --- |
| `api` | `apiURL` |
| `direct` | `env`, `provider`, `region`, `stateBucket` |
| `skiffd` | `env`, `provider`, `region`, `stateBucket` |
| `runner` | `env`, `provider`, `region`, `stateBucket`, `service`, `controlKey` |

AWS live-apply inputs are provider configuration, not service-spec fields. Set
`awsLiveApply: true` only when requesting SDK-backed live apply. `awsVpcId`,
`awsSubnetIds`, and `awsAmiId` are required for live target groups, Auto
Scaling Groups, and launch templates. `awsAlbListenerArn` is required only when
the service has an ingress listener rule. `awsLoadBalancerSecurityGroupRef` is
required when service VM ingress uses the `load-balancer` source.

Runner user-data is strict JSON under a top-level `skiff` object:

```json
{
  "skiff": {
    "env": "prod",
    "service": "payments-api",
    "provider": "aws",
    "region": "us-west-2",
    "state_bucket": "s3://skiff-state-prod",
    "control_key": "services/payments-api/control.json"
  }
}
```
