# Private hosted pilot qualification — 2026-09-24

The private hosted pilot is deployed at `https://ferro.sire.run`. TLS, wake, authenticated MCP initialization and `tools/list` have passed live checks. The service is currently asleep at desired count zero; full browser pairing and end-to-end work through Chrome remain open. Local Ferro 0.1.9 is installed and the user-reloaded Chrome extension is connected to its local service.

## Accepted local checks

- Runtime through `50dbf27`: full `go test -race ./...`, `go vet ./...`, semantic contract validation and all 18 frozen contract hashes pass.
- Extension through `e8911f4`: 51 tests pass with `FERRO_TEST_BROWSER=1`, including real Chrome handoff. Fresh reviewer accepted the final captured-generation correction.
- Infrastructure through `37eb77c`: all three nested module packages pass with `GOWORK=off`, resolving the published AMSL dependency. Fresh final infrastructure review reports no concrete findings.
- ARM64 non-root image built from runtime `50dbf27`, read-only root filesystem. Local container checks: health 200; missing bearer 401; invalid host 403; missing trusted HTTPS indication 403; authenticated chat status 200; MCP initialize 200.
- Initial ECR image digest: `sha256:fca2fdf469be1bab75ca3be1708b57e8f901d0b76d2622053f1045dee750b873`.
- Hosted runtime image digest: `sha256:10ba6050aea1c82bcf3c3e8bdf20a20726bc4c0a01d5eb972123a5f7de480775`.

## Live resources and blocker

Cloudflare is authoritative for `sire.run`; the zone is active, the 23 additional verified live records were imported, and `ferro.sire.run` plus its ACM validation record were added. ACM issued the certificate. The Pulumi pilot stack is activated in AWS `us-west-2`: it created the private Fargate service, encrypted retained EFS home/access point, authenticated Lambda wake route, task and execution roles, logs, listener rules, and a single TCP 8080 egress rule from the shared ALB security group to Ferro's dedicated task security group. The service starts at desired count zero and wakes on authenticated `POST /bridge/wake`.

Live checks on 2026-09-24: `/healthz` returned HTTP 200; authenticated `/mcp` initialization and `tools/list` returned HTTP 200; unauthenticated MCP access returned HTTP 401; wake returned HTTP 202 and started the service. CloudTrail confirmed EFS `ClientMount` and `ClientWrite` permissions on the task role/access point. After the idle interval, ECS returned to desired count zero with no running or pending tasks, as configured; requests before the next authenticated wake are expected to receive 503 while it sleeps.

Activation exposed and corrected four integration defects: the AWS account's Lambda concurrency minimum rejected a fixed reservation; EFS resource-policy conditions denied mounting; Fargate's task-specific `ECS_AGENT_URI` includes `/api/<task-id>`; and the shared ALB security group had no egress permission for Ferro's new task security group. Each correction is in the repository's Pulumi/runtime source. The original pilot image remains in ECR; the running stack references the updated immutable digest above.

Foundation DNS PRs [250](https://github.com/sirerun/foundation/pull/250) and [251](https://github.com/sirerun/foundation/pull/251) were merged earlier, but their Google DNS workflow did not run because of the account billing restriction. The zone migration and record additions were completed directly in Cloudflare instead; no further Google Cloud DNS changes are needed for this pilot.

## Review corrections

Independent reviews and coordinator checks corrected reconnect poll restart, foreground/background connection priority, captured-generation cleanup, repeated wake after a delayed sleep, idle timestamp recheck under admission exclusion, exact ECS wake cluster, supported ECS/Lambda trust conditions, EFS task-role/access-point enforcement, ALB egress/listener dependencies and wake-rule precedence. Browser actions are never automatically replayed after a lost response.

## Still required before declaring hosted use ready

1. Pair a Chrome tab to the hosted bridge and verify read-only snapshot/browser status from an MCP client; the live MCP smoke test did not pair a real tab.
2. Exercise a connected tab and active task/lease, then disconnect and observe the service drain to desired count zero; reconnect and confirm it wakes without redispatching actions.
3. Verify EFS single-owner exclusion, receipt persistence, crash uncertainty and restart behavior.
4. Record cold-start duration, AWS resource inventory, operating cost, and rollback evidence. Preserve EFS and credentials on rollback.

The AWS deployment identity component is a pinned, human-approved AMSL candidate. Its optional consumer integration is present; the pilot bootstrap used the existing operator identity. Live GitHub OIDC assumption/denial and component adoption qualification remain pending. Account/auth/billing work is owned by the separate Go AMSL effort.
