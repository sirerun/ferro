# Ferro Cloud container image

This is a minimal non-root image for the single-owner `ferro-cloud` service. It builds with the official Go 1.25.5 Alpine image pinned by multi-platform index digest and runs a statically linked `CGO_ENABLED=0` binary in `scratch` with only the CA bundle copied into the runtime image. It does not include Chrome or a browser binary; the service uses the paired extension backend.

**Status: packaging and runtime qualification are pending.** The image has not been built or deployed as part of this change. This image does not provide AWS provisioning, scale-to-zero, or a wake-up path.

Build from the repository root so the Dockerfile can read the Go module and source tree:

```sh
docker build --file deploy/container/Dockerfile --tag ferro-cloud:local .
```

For an ALB-backed deployment, set `FERRO_CLOUD_LISTEN_ADDR=0.0.0.0:8080` and explicitly set `FERRO_CLOUD_TRUSTED_PROXY_CIDRS` to the canonical private subnet CIDRs that contain the trusted proxy/ALB peers. The CIDR list is comma-separated with no whitespace. Only canonical prefixes contained wholly in RFC1918, IPv4 loopback, IPv6 ULA, or IPv6 loopback ranges are accepted. Public and default-route ranges such as `0.0.0.0/0` are rejected. Without explicit proxy CIDRs, the listener remains loopback-only and only loopback peers can establish forwarded HTTPS. Never add a broad range to make proxy validation pass.

Keep `FERRO_CLOUD_PUBLIC_ORIGIN=https://ferro.sire.run`; public requests still require the exact configured Host and HTTPS indication from a trusted peer. The exception is the minimal `GET /healthz` check: a trusted peer can query it over plain HTTP using an IP literal Host, as ALB target health checks do. Other hosts and methods do not receive the health response. The endpoint reports only `ok` and does not inspect task, credential, browser, or receipt state.

Mount a persistent private filesystem at `/var/lib/ferro-cloud` and set its access point or directory ownership to UID/GID `65532:65532`. The runtime process uses that identity and `FERRO_MCP_HOME` points to the mount. Keep one service process per private home; receipt/profile/credential state is single-owner and is not a shared multi-replica database. Do not deploy the container with ephemeral task storage for the private home.

The legacy `FERRO_CLOUD_MCP_TOKEN` and `FERRO_CLOUD_BRIDGE_TOKEN` variables continue to name private token files. For ECS Secrets Manager value injection, use `FERRO_CLOUD_MCP_TOKEN_VALUE` and `FERRO_CLOUD_BRIDGE_TOKEN_VALUE` instead. A token value must be at least 32 printable ASCII characters with no surrounding whitespace. Supplying a file path and value for the same token is rejected. Values are written once into the private home with mode `0600`; subsequent starts must present the same value or startup fails with a redacted conflict error. MCP and bridge values must be distinct. Do not echo secrets into task logs or container arguments.

This image expects the extension bridge to be paired by its separately authorized client workflow. It adds no public signup, user management, billing, standalone authentication, browser installation, or container wake logic. The observed 60-second HTTP idle timeout is unchanged; any pilot request deadline belongs to the separately scoped rollout/client work.
