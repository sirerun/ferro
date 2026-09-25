# Ferro hosted AWS pilot

This Pulumi program provisions an isolated, scale-to-zero ECS Fargate service and a small authenticated wake endpoint behind an existing shared HTTPS Application Load Balancer. It does not create or modify the shared load balancer, its listener, DNS, or other services. It adds one narrowly scoped egress rule to the supplied ALB security group so the ALB can reach the dedicated Ferro task security group on TCP 8080. The bootstrap repository and certificate have been created; see the [qualification record](../../docs/evidence/hosted-pilot/qualification.md) for exact live checks.

## Requirements

Use Pulumi CLI and AWS credentials for the intended account and region. The account must already contain the VPC, public subnets with outbound internet through an internet gateway, the shared ALB security group, and an HTTPS listener with a default rule. The supplied subnet set must cover at least two Availability Zones for EFS mount targets. The selected ALB listener rule priorities must be free. Confirm them against the current listener rules before activation.

Build and push the application image separately to the ECR repository created in bootstrap. Activate with the immutable `sha256:` digest returned by the registry. The image must be Linux ARM64, run as UID/GID 65532, support the configured proxy trust variables and `/healthz`, and accept the `FERRO_CLOUD_MCP_TOKEN_VALUE` and `FERRO_CLOUD_BRIDGE_TOKEN_VALUE` environment variables. The container writes durable private state under `FERRO_MCP_HOME`; `/tmp` is ephemeral.

## Bootstrap, DNS, and activation

Select a dedicated Pulumi stack and configure `aws:region` and `hosted:region` to the same explicit region. Set `hosted:stage` to `bootstrap`, and provide `hosted:accountId`, `hosted:publicDomain`, and `hosted:albDnsName`. Bootstrap creates the immutable image repository and protected DNS-validated ACM certificate. It exports the repository URL, certificate ARN, and ACM validation record details.

Publish the ACM validation CNAME records through the separately managed DNS workflow, then wait for ACM to mark the certificate issued. This program does not create DNS records. The public application hostname and wake path must be routed through the supplied shared ALB.

For activation, set `hosted:stage` to `activate` and supply:

| Key | Value |
| --- | --- |
| `hosted:imageDigest` | The pushed image's full `sha256:` digest; tags are not accepted. |
| `hosted:vpcId` | Existing VPC ID. |
| `hosted:publicSubnetIds` | JSON array of at least two distinct public subnet IDs. |
| `hosted:albSecurityGroupId` | Security group attached to the shared ALB. |
| `hosted:albListenerArn` | Existing HTTPS listener ARN in the configured account and region. |
| `hosted:albDnsName` | Canonical DNS name of the shared ALB. |
| `hosted:trustedProxyCIDRs` | JSON array of exact private/loopback proxy subnet CIDRs used by the ALB. Never use a catch-all range. |
| `hosted:validationRecordFqdns` | JSON array of ACM DNS validation record names after external DNS publication. |
| `hosted:wakeZipPath` | Path to the Go custom-runtime Lambda zip artifact. |
| `hosted:wakeRulePriority`, `hosted:appRulePriority` | Distinct unused listener priorities; defaults are 43000 and 43001. |

Build the wake artifact from this directory with Go targeting Linux ARM64, then package the `bootstrap` executable at the zip root:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bootstrap ./cmd/wake
zip wake.zip bootstrap
```

Before activation, inspect the current shared listener rules and confirm both selected priorities are unused. Preview the stack and review all changes, especially the shared listener attachments and the retained EFS filesystem. Apply only through the installation's reviewed change process. The service is created with desired count zero; activation alone does not mean the ALB path, credential injection, task protection, or wake lifecycle has passed a live qualification.

## Runtime and secrets

The runtime task is non-root and uses a read-only root filesystem. EFS is encrypted, protected from deletion, mounted through an access point owned by UID/GID 65532, and restricted to TLS plus IAM authorization. The task security group accepts port 8080 only from the supplied ALB security group; the EFS security group accepts NFS only from the task security group. Tasks receive public IPs in the supplied public subnets to avoid a NAT gateway; their egress remains governed by the task security group and subnet routing.

Pulumi generates distinct 64-character bridge and MCP credentials and stores them in Secrets Manager. ECS injects them through the container's `*_TOKEN_VALUE` environment variables. Pulumi exports the generated values as secret outputs. Keep them masked in CI and do not print secret outputs in routine logs. A designated operator can retrieve them through the approved secrets-handling process; do not use `--show-secrets` in shared or captured terminal sessions.

The wake Lambda accepts only authenticated `POST /bridge/wake` requests for the configured host, ignores caller-provided AWS targets, and asks ECS to set this service's desired count to one. Its `202` means the request was accepted, not that the task is healthy or ready. The service returns to desired count zero through the separately managed idle lifecycle. The ALB's shared idle timeout is left unchanged.

Application and wake logs are retained for 14 days. EFS and ACM resources are protected/retained; plan their lifecycle explicitly before any eventual teardown.
