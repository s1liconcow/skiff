# mTLS Plugin

Skiff's mTLS support is an explicit capability plugin. It is not enabled by default and it does not install a global service mesh.

Enable it with an `addons` entry in the service spec and pass the plugin manifest when explaining or planning:

```bash
skiff plugin validate plugins/mtls --format json
PATH="$PWD/bin:$PATH" skiff explain examples/plugins/mtls/strict.yaml --plugin plugins/mtls --format json
```

The plugin emits typed patches only:

- `SecurityGroupRule` patches for explicit service-to-service egress.
- `ListenerMTLS` patches for ingress client-certificate verification.
- `IAMRoleSecretRef` patches for least-privilege workload certificate access.
- a `systemd-unit` runtime addon for the VM-local mTLS proxy and certificate agent.
- doctor findings for proxy health, certificate expiry, expired certificates, and policy mismatch.

Example strict config:

```yaml
addons:
  - name: mtls
    mode: strict
    config:
      certificateSecretRef: aws-secretsmanager://arn:aws:secretsmanager:us-west-2:111122223333:secret:skiff/prod/payments-api/mtls-workload-cert
      ingress:
        clientCertificate:
          mode: verify
          trustStoreRef: aws-elbv2-trust-store://us-west-2/payments-client-ca
      outbound:
        - service: orders-api
          port: 8443
```

The plugin keeps cloud primitives visible. `skiff explain --plugin plugins/mtls --format json` includes the plugin patch explanations, the lowered AWS listener fields `client_certificate_mode` and `trust_store_ref`, security group egress, and the IAM secret access needed by the workload certificate.
