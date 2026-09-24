# Private container pilot

Status: implementation in progress; not deployed or provider-qualified.

## Accepted product behavior

The user selected Pulumi-managed AWS containers and explicitly approved sleeping
only when no browser tabs are connected. Connect wakes the service. One paired
tab keeps the process warm; disconnect or expired authenticated heartbeat allows
sleep after active work, leases and dispatched commands drain. This preserves the
single-owner executor. It does not claim multi-tab execution or between-task sleep.

The pilot uses distinct private bridge and MCP bearer credentials. Accounts,
authentication and billing remain a separate AMSL adoption effort. Fixed-auth
pilot access is not a public paid launch.

## Infrastructure and ownership

A dedicated ECS Fargate service has desired count zero or one. Its runtime owns
desired count; ordinary IaC updates must ignore that field. Deployment uses
stop-before-start settings, with both process and receipt locks on retained,
encrypted EFS storage. EFS requires TLS, IAM authorization and a non-root access
point. Task ingress permits only the shared ALB security group. Persistent data
must survive task replacement; no relay socket is served from the hosted home.

The shared ALB receives only exact Ferro host/path rules and an additional SNI
certificate. Its global 60-second idle timeout remains unchanged. Cloud execution
is capped at 45 seconds, queued HTTP requests at 50 seconds, leaving room for
terminal receipt persistence. The effective model profile/revision records the
execution cap. Long-lived MCP GET event streams do not keep compute awake.

An authenticated wake Lambda requests desired count one for exactly this service.
It returns requested/starting, never ready. The extension retries readiness and
pairing within a bounded cold-start window. The runtime establishes ECS task
scale-in protection before readiness and renews it while serving; a delayed sleep
request cannot terminate a newly protected active task. After atomic idle drain,
new work is rejected, protection is removed and desired count becomes zero.
Ambiguous control-plane failures keep admission closed. Protection does not make
process crashes or infrastructure failures impossible; interrupted work remains
uncertain and must be reconciled from receipts.

Foundation owns an isolated DNS stack for the Ferro CNAME and its ACM validation
CNAME. AWS bootstrap first produces reviewed DNS outputs; the Foundation workflow
previews and applies only those records; AWS activation follows certificate
issuance. No other product's application or production stack is redeployed.

## AMSL dogfood

`ajent-social/pulumi` PR #1 supplies the human-reviewed candidate AWS deployment
identity component. Its exact preview/apply contexts and explicit permissions are
consumed through a pinned module, not copied. Its limited supported policies do
not authorize arbitrary infrastructure provisioning: the initial stack uses the
existing operator bootstrap identity. Any configured automation authority must
be tested against its actual consumer operations. Component mocks, provider
preview, denied trust and real consumer execution are separate evidence.

## Implementation ownership

- Container packaging, proxy trust and runtime composition: coordinator integration.
- Generic idle/drain lifecycle and bridge activity checks: lifecycle lane.
- Pulumi resources and authenticated wake Lambda: infrastructure lane.
- Extension cold-start and reconnect UI: integration after reviewed local runtime.
- Isolated DNS workflow: Foundation branch, separately reviewed.

## Required qualification

1. Fresh review and tests for authentication, exact host/proxy trust, wake target
   restriction, task deadlines, idle admission and renewal-failure draining.
2. Build the pinned non-root image and exercise its health/auth boundary locally.
3. Review Pulumi preview: only dedicated pilot resources and narrow ALB additions;
   verify retained state and exact permissions. Apply through IaC.
4. Verify DNS/TLS and rejected credentials. Verify a real Chrome tab and fresh MCP
   client through the hosted endpoint without paid/provider or account mutations.
5. Verify Connect from desired count zero, single process ownership, receipt
   persistence across replacement and two-task exclusion on actual EFS.
6. Verify active tab/task/lease prevents sleep, disconnect drains to zero, repeated
   wake during draining recovers, and old receipt IDs never redispatch work.
7. Record resource inventory, tested image digest, rollback and observed cold-start
   time. Rollback must preserve EFS receipts and credentials.

Scale-to-zero refers to application compute. ALB, retained storage, secrets and
other AWS resources can still incur charges while the container is stopped.
