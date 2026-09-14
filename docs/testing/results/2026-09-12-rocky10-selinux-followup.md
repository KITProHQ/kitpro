# Rocky Linux 10 SELinux follow-up: 2026-09-12

## Run identity

- Overall status: `PASS` for Docker container SELinux confinement; helper-domain design remains `NOT RUN`
- VM: Proxmox VM 501, `10.10.0.114`
- OS: Rocky Linux 10.2 (Red Quartz)
- Starting checkpoint: guest state matched `docker-installed`; Josh reported VM 501 ready after the requested restore
- Snapshot lineage: `OBSERVATION` — the automation account could not query Proxmox snapshots, so the guest state was verified directly
- Repository baseline: `8e8e05176b5f9c2669d702b679e034828bdc08f4`
- Corrected fixture run ID: `484021b3-9834-44b2-94d8-973883deaf26`
- Previous immutable result: [Rocky Linux 10 validation](2026-09-12-rocky10-validation.md)
- Procedure: [privilege-boundary runbook](../../../prototypes/privilege-boundary/RUNBOOK.md)

This follow-up preserves the earlier failed SELinux evidence. It does not rewrite that run as a pass. It isolates the cause, applies one documented Docker daemon setting, and reruns the corrected helper fixture with SELinux Enforcing.

## Root cause and remediation

| Test | Status | Observation |
| --- | --- | --- |
| SELinux host mode | PASS | Targeted policy remained `Enforcing` throughout. |
| Container policy package | PASS | `container-selinux-2.246.0-1.el10.noarch` was installed and its policy module was loaded. |
| Runtime domain | PASS | `dockerd` and `containerd` ran in `container_runtime_t`. |
| Pre-remediation Docker options | FAIL | Docker reported seccomp and cgroup namespaces only; `name=selinux` was absent. |
| Pre-remediation container labels | FAIL | The previous run produced empty `ProcessLabel` and `MountLabel` and a host process in `spc_t`. |
| Root cause | OBSERVATION | Docker CE's RHEL-compatible package installed the policy dependency but did not enable daemon SELinux integration. `/etc/docker/daemon.json` was absent and the packaged unit supplied no SELinux-enable flag. |
| Remediation | PASS | Added the documented `{"selinux-enabled": true}` daemon configuration after `dockerd --validate` accepted it, then restarted Docker. No policy or enforcement setting changed. |
| Post-remediation Docker options | PASS | Docker reported `name=seccomp`, `name=selinux`, and `name=cgroupns`. |
| Post-remediation process label | PASS | Disposable probe used `system_u:system_r:container_t:s0:c224,c415`; the full helper run used `container_t` with a separate MCS pair. |
| Post-remediation mount label | PASS | Disposable probe used `system_u:object_r:container_file_t:s0:c224,c415`; the full helper run also received a non-empty `container_file_t` label. |
| AVC review | PASS | `ausearch` returned no AVC or USER_AVC records for the remediation and corrected run. |

The remediation did not require `setenforce`, a permissive domain, `chcon`, `audit2allow`, a custom policy, or a firewalld change.

## Corrected full fixture

| Test | Status | Observation |
| --- | --- | --- |
| Host gate | PASS | Rocky 10.2, x86_64, systemd PID 1, cgroup v2, XFS, 31.5+ GiB free, SELinux Enforcing, firewalld active. |
| Docker authority | PASS | Root/helper reached Docker; API UID 993 and `nobody` could not. |
| Helper socket | PASS | Runtime directory `root:kitpro-pb-api-test` 0750; socket `root:kitpro-pb-api-test` 0660. |
| Peer credentials | PASS | Allowed peer UID was 993; root was rejected by helper policy even after reaching the socket. |
| Local suite | PASS | 35 of 35 tests passed on Rocky. |
| Dangerous input | PASS | Unknown, malformed, oversized, stale, raw-Docker, Compose, shell, path, mount, device, capability, namespace, security-option, and sysctl requests were rejected before Docker dispatch. |
| Container shape | PASS | Digest-pinned BusyBox, UID/GID 65534, read-only root, all capabilities dropped, `no-new-privileges=true`, no binds/devices/ports, and one internal network. |
| Docker SELinux gate | PASS | Runner required `name=selinux`, non-empty Docker labels, and a host process that was not `spc_t`. |
| Stop behavior | PASS | The one-second BusyBox PID 1 loop stopped normally; the earlier long-sleep forced-stop behavior was a fixture defect. |
| Docker outage | PASS | With both `docker.service` and `docker.socket` stopped, helper returned retryable `DockerUnavailable`; the request did not reactivate Docker. Explicit restart restored inspection. |
| Helper restart/replay | PASS | Exact UUID/body replay survived helper restart; conflicting reuse returned `OperationConflict`. |
| Cleanup in runner | PASS | Managed container/network were removed; the exact disposable unrelated control was removed separately. |

The runner initially stopped because Docker 29 represented the enforced security option as `no-new-privileges=true`, while the test asserted only `no-new-privileges`. The assertion now accepts either true spelling and still rejects absence or false. This was a test-harness compatibility defect, not a relaxation of container policy.

## Stopped foreign endpoint

Status: `PASS` end to end.

The helper created a managed stopped container and its per-instance bridge. A separately created, disposable stopped container was attached to the same network. Docker network inspection reported zero members, while `docker ps -a --filter network=<exact-name>` reported both endpoints. The corrected adapter therefore detected two members, and the helper returned `PolicyDenied` without changing either container. After exact removal of the foreign control, the helper removed its managed resources normally.

This confirms that destructive ownership checks must enumerate all containers associated with the network. Docker's network-inspect member map is not a sufficient source for stopped endpoints.

## Helper SELinux domain

| Item | Status | Observation |
| --- | --- | --- |
| Current helper domain | FAIL for production posture | The test helper still ran as `system_u:system_r:unconfined_service_t:s0`. |
| Current path types | OBSERVATION | Fixture source `usr_t`, state `var_lib_t`, runtime/socket `var_run_t`, Docker socket `container_var_run_t`. |
| Dedicated domain feasibility | OBSERVATION | Feasible in principle, but the required Docker-socket permission grants powerful Engine control and must be measured from a packaged executable with dedicated state/runtime types. |
| Test-only custom policy | NOT RUN | No narrow policy was installed. Creating one before the executable path, packaged labels, audit path, and production operation set exist would produce misleading or overly broad rules. |

A later policy experiment should define a helper executable transition, dedicated runtime/state/audit/storage types, local socket access, and only the Docker socket permissions actually observed. Docker-socket access remains powerful host authority; SELinux does not replace the semantic protocol and ownership checks.

## systemd hardening findings

- `Requires=docker.service` remains removed. `After=docker.service` preserves ordering without turning a helper request into Docker activation.
- `RestrictSUIDSGID=yes` remains omitted from the disposable unit because Rocky systemd 257 caused the required `openat2` path check to return `ENOSYS` when that control was present. The setting is defense in depth, while descriptor-relative `openat2` containment is a primary filesystem boundary.
- The omission is not approval to drop hardening from production. A later unit review must reproduce the systemd/seccomp interaction and choose a narrower replacement or an upstream-compatible configuration.
- Other measured controls remained active, including an empty capability bounding set, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateDevices`, namespace restrictions, address-family restrictions, resource limits, and environment sanitization.

## Remaining limitations

- Dedicated helper SELinux domain and path types: `NOT RUN`.
- Arbitrarily interrupted real Docker mutation and automated reconciliation: `NOT RUN`.
- High-frequency link/mount race harness: `NOT RUN`.
- IPv4/IPv6 loopback publication: `NOT RUN`; the constrained prototype has no publication operation.
- Package upgrade behavior with the daemon setting: `NOT RUN`.
- Proxmox snapshot lineage verification by automation: `BLOCKED` by current Proxmox account permissions.
