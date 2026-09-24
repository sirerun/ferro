# ADR 010: Private single-owner hosted pilot

Status: authorized implementation, deployment pending verification. Date: 2026-09-24.

The user requests an immediately usable hosted MCP service on existing AWS resources while a separate Cursor session builds AMSL auth, users and billing. This is a separate private pilot, not a paid public launch or a replacement identity system.

Use one Ferro owner/process and one paired browser tab. The same existing private installation identity owns receipts across authenticated MCP sessions. Require distinct high-entropy MCP and extension bearer secrets supplied privately at runtime, HTTPS at the edge, a persistent private service home, and no automatic retries after uncertain dispatch. Secrets must not be compiled into code, images, extension packages or committed configuration.

Expose `/mcp` and a reverse-proxied `/bridge/` path from one explicitly configured public origin. The bridge continues listening on loopback; the proxy rewrites its upstream Host for existing chat checks. Preserve localhost extension mode and add only the explicit Ferro HTTPS endpoint. Keep the existing Tailscale listener unchanged.

AMSL account authority, tenant routing, billing and multi-tab execution remain separate adoption work. This pilot does not satisfy their launch gates. Public deployment requires tested transport/authentication, an inspected isolated AWS target, trusted TLS, persisted state, and a rollback path that preserves receipts. Do not redeploy or change another product's application.

## Deployment constraint added during implementation

The user selected container-based AWS infrastructure managed entirely through
Pulumi, with scale-to-zero behavior and a reusable contribution to the AMSL
Pulumi repository. The exploratory dedicated-EC2/CloudFormation path is
superseded and must not be deployed. The Caddy/systemd files currently beside
the transport are examples of its proxy contract, not the selected deployment.

A zero desired task count alone does not establish a working cold-start path.
The current owner must process MCP tasks concurrently with extension polls and
replies, and receipts must survive process replacement. A single-concurrency
Lambda wrapper would prevent those concurrent requests; multiple independent
owners would lose command routing. The deployment design must explicitly
qualify wake-up, in-flight protection, durable state, reconnect and uncertainty
before claiming scale-to-zero readiness. Existing fixed-auth transport work
remains useful and is independent of that infrastructure decision.
