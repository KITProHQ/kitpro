# Rocky Linux 10 helper SELinux validation — 2026-09-12

Status: **PASS (test-only feasibility)**. This immutable record covers a disposable dedicated-domain experiment on Rocky Linux 10. It does not establish production policy packaging, upgrade, or rollback compatibility.

## Environment

- Rocky Linux 10.2, x86_64; kernel `6.12.0-211.54.1.el10_2.x86_64`; systemd 257; cgroup v2; XFS.
- SELinux remained `Enforcing`; firewalld remained active.
- Docker Engine 29.8.0 with `name=selinux`; test containers received normal SELinux labels.
- Test-only policy build tooling (`policycoreutils-devel`, `selinux-policy-devel`, `make`, `gcc`) was installed; no production policy was installed.

## Policy and context evidence

The module compiled and loaded after two portability corrections: `files_type` replaced an unavailable `files_runtime_file` macro, and `init_t` replaced an unavailable `systemd_t` type. A narrow systemd runtime-directory permission was required for socket activation. An explicit temporary `SELinuxContext` drop-in was used to enter the dedicated helper domain; the policy added the measured executable transition and NSS/read permissions needed by the fixture.

| Object | Measured context |
| --- | --- |
| Helper process | `system_u:system_r:kitpro_pb_helper_t:s0` |
| Helper executable | `kitpro_pb_helper_exec_t` |
| State | `kitpro_pb_state_t` |
| Approved storage | `kitpro_pb_storage_t` |
| Runtime/socket | `kitpro_pb_runtime_t` |
| Docker socket | `container_var_run_t` |
| Test container process/mount | `container_t` / `container_file_t` with MCS categories |

## Results

| Case | Status | Evidence |
| --- | --- | --- |
| Enforcing mode preserved | PASS | `getenforce` remained `Enforcing` before, during, and after testing. |
| Policy compile/load and relabel | PASS | `make`/`semodule`/`restorecon` completed after the documented corrections. |
| Socket, peer credentials, helper→Docker | PASS | API UID 993 ping, runtime inspection, and Docker Engine operations succeeded in the dedicated domain. |
| Container/network lifecycle | PASS | Internal per-instance network and disposable container lifecycle succeeded without host ports. |
| Helper restart and Docker outage/recovery | PASS | Socket activation and bounded Docker-unavailable/recovery behavior passed. |
| MAC negative probes | PASS | Disposable unrelated storage read, shell execution, and IP connect were denied. |
| AVC denials | PASS | `ausearch -m AVC,USER_AVC -ts recent` returned no fixture-related matches. |
| Exhaustive ownership disagreement suite under dedicated domain | NOT RUN | Semantic ownership matrix was previously PASS; this run covered combined MAC create/remove, not every disagreement case. |
| Production policy/package upgrade/rollback | NOT RUN | Follow-up required. |

No `setenforce`, permissive domain, broad `audit2allow`, or firewall weakening was used. The module, temporary systemd drop-in, labels, and disposable resources were removed after capture; SELinux remained Enforcing.

## Conclusion

Rocky helper confinement is technically viable with a dedicated SELinux domain, but remains experimental until policy packaging/lifecycle, daemon configuration preservation, firewalld regression, upgrade, and rollback tests pass.
