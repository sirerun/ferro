# Private hosted pilot qualification — 2026-09-24

This record separates local qualification from live cloud readiness. The hosted endpoint is **not yet usable**. Local Ferro 0.1.9 is installed and the user-reloaded Chrome extension is connected, idle and unleased.

## Accepted local checks

- Runtime through `50dbf27`: full `go test -race ./...`, `go vet ./...`, semantic contract validation and all 18 frozen contract hashes pass.
- Extension through `e8911f4`: 51 tests pass with `FERRO_TEST_BROWSER=1`, including real Chrome handoff. Fresh reviewer accepted the final captured-generation correction.
- Infrastructure through `37eb77c`: all three nested module packages pass with `GOWORK=off`, resolving the published AMSL dependency. Fresh final infrastructure review reports no concrete findings.
- ARM64 non-root image built from runtime `50dbf27`, read-only root filesystem. Local container checks: health 200; missing bearer 401; invalid host 403; missing trusted HTTPS indication 403; authenticated chat status 200; MCP initialize 200.
- ECR image digest: `sha256:fca2fdf469be1bab75ca3be1708b57e8f901d0b76d2622053f1045dee750b873`.

## Live resources and blocker

Pulumi bootstrap created only the dedicated image repository and ACM certificate request. The immutable image was pushed. Shared ALB listener priorities were checked and remain free; two configured public subnets are in distinct availability zones in the existing VPC. No ECS service, EFS home, Lambda wake function or ALB routing has been activated.

Foundation DNS changes are merged in PRs [250](https://github.com/sirerun/foundation/pull/250) and [251](https://github.com/sirerun/foundation/pull/251). The DNS preview [run](https://github.com/sirerun/foundation/actions/runs/36067834673) never started: GitHub reports failed account payments or spending limit. Local Google Cloud authentication also requires renewal. Either restore Actions billing or renew authorized local Google Cloud login before publishing the two isolated DNS records through Pulumi. No DNS change was made.

## Review corrections

Independent reviews and coordinator checks corrected reconnect poll restart, foreground/background connection priority, captured-generation cleanup, repeated wake after a delayed sleep, idle timestamp recheck under admission exclusion, exact ECS wake cluster, supported ECS/Lambda trust conditions, EFS TLS/access-point/role enforcement, ALB permission/listener resource dependencies and wake-rule precedence. Browser actions are never automatically replayed after a lost response.

## Still required before declaring hosted use ready

1. Exact-scope activation apply after DNS validation and ACM issuance. The provider preview passed: 33 resources to create, three unchanged, no updates or deletions of existing resources.
2. Live TLS, invalid credentials, wake/readiness and fresh MCP initialization.
3. Actual EFS single-owner exclusion, receipt persistence, crash uncertainty and restart tests.
4. Real Chrome pairing and read-only example-page interaction over the hosted bridge.
5. Observe connected tab/task/lease staying warm; disconnect drains desired count to zero; reconnect wakes successfully without action redispatch.
6. Record actual cold-start duration, AWS resource inventory and rollback evidence. Preserve EFS and credentials on rollback.

The AWS deployment identity component is a pinned, human-approved AMSL candidate. Its optional consumer integration is present; the pilot bootstrap used the existing operator identity. Live GitHub OIDC assumption/denial and component adoption qualification remain pending. Account/auth/billing work is owned by the separate Go AMSL effort.
