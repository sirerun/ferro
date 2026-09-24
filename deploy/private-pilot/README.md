# Private hosted pilot transport examples

Deployment is not live. The selected AWS deployment must use Pulumi-managed
containers with a qualified scale-to-zero and wake-up path; see [ADR 010](../../docs/adr/010-private-hosted-pilot.md). The files here demonstrate the transport’s trusted HTTPS-proxy and private-state requirements. They are not authorization or instructions to provision an always-on EC2 host.

These templates assume one `ferro-cloud` process, one persistent
`FERRO_MCP_HOME`, and one explicitly paired Chrome tab. Do not run multiple
replicas against this state directory.

Build and install `ferro-cloud` at `/usr/local/bin/ferro-cloud`, install the
systemd unit, and use Caddy to terminate HTTPS for `ferro.sire.run`. The Go
service listens only on loopback and trusts `X-Forwarded-Proto: https` only
from a loopback peer. Keep the reverse proxy on the same host.

Optional `EnvironmentFile` entries may point to existing private token files:

```sh
FERRO_CLOUD_MCP_TOKEN=/etc/ferro-cloud/mcp-token
FERRO_CLOUD_BRIDGE_TOKEN=/etc/ferro-cloud/bridge-token
```

Each file must be a regular file with mode `0600` and contain at least 32
characters. When omitted, the service creates credentials under
`/var/lib/ferro-cloud`. Do not put token contents in this environment file or
in service command-line arguments.

The pilot exposes `/mcp`, `/bridge/*`, and a minimal `/healthz`. It has no
public signup, billing, or multi-user support.
