# Rocky Linux 10 SELinux validation

## Status

The initial 2026-09-12 Rocky Linux 10.2 run `FAILED` the intended mandatory-access-control posture. SELinux remained `Enforcing`, but Docker did not enable SELinux integration. The helper ran as `unconfined_service_t`, and the test container ran as `spc_t` with empty Docker process and mount labels.

The [SELinux remediation follow-up](results/2026-09-12-rocky10-selinux-followup.md) `PASSED` Docker workload confinement after enabling Docker's documented `selinux-enabled` daemon setting. Docker then reported `name=selinux`, and workloads received `container_t` process and `container_file_t` mount labels with MCS categories. The later [dedicated helper-domain result](results/2026-09-12-rocky-helper-selinux.md) `PASSED` test-only feasibility under Enforcing with dedicated executable, state, runtime, and storage types; production policy packaging and upgrade validation remain open.

These results are not a reason to disable SELinux. They show that checking `getenforce` or installing `container-selinux` alone is insufficient.

## Boundary tested

The experiment crossed four boundaries:

1. an unprivileged test client opened a systemd-owned Unix socket;
2. the root helper read root-owned fixture state and wrote only the fixed fixture state and storage roots;
3. the helper opened Docker Engine's local Unix socket and used its closed API subset; and
4. Docker created a non-root, read-only test container on an internal user-defined bridge.

Unix owner, group, mode, and `SO_PEERCRED` checks worked. They do not replace SELinux confinement.

## Measured contexts

| Object or process | Measured context or result | Assessment |
| --- | --- | --- |
| API-test client | `unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023` | Test identity had DAC isolation but no dedicated SELinux domain. |
| Helper process | `system_u:system_r:unconfined_service_t:s0` | Unacceptable as the intended production helper boundary; a packaged domain remains necessary. |
| Fixture source under `/opt` | `unconfined_u:object_r:usr_t:s0` | Generic test labeling. |
| State root | `var_lib_t` | Generic state labeling; production needs a KITPro-specific persistent type. |
| Runtime directory and helper socket | `var_run_t` | Generic runtime labeling; production needs a KITPro-specific runtime/socket type. |
| Docker socket | `container_var_run_t` | Package-provided Docker runtime type. |
| `dockerd` and `containerd` | `container_runtime_t` | Expected runtime domain. |
| Test container host process | `system_u:system_r:spc_t:s0` | Unconfined container domain; required normal container confinement was absent. |
| Docker `ProcessLabel` and `MountLabel` | Both empty | Confirms Docker did not request SELinux labels for the container. |
| Docker security options | seccomp and cgroup namespaces only | `name=selinux` was absent. |

The host had `container-selinux-2.246.0-1.el10.noarch`. `/etc/docker/daemon.json` was absent, and Docker's packaged systemd unit started `dockerd` without an SELinux enablement option. Installing the policy package therefore did not, by itself, enable Docker container labeling.

## AVC evidence

`auditd` was active. `ausearch -m AVC,USER_AVC -ts today -i` returned no matches during the fixture run. No AVC was bypassed and no policy module was generated.

The absence of AVCs is expected when the participating processes are unconfined. It is not evidence that the desired SELinux policy already works.

## What worked under Enforcing mode

- Docker installation completed without changing SELinux mode.
- The root-owned Unix socket and DAC/peer-credential boundary worked.
- The helper reached the Docker socket.
- Docker lifecycle and ownership-negative tests ran.
- firewalld remained active.
- helper, Docker, and full VM restart tests completed.
- no `setenforce`, permissive domain, `chcon`, broad policy, or `audit2allow` workaround was used.

These results establish feasibility for the Linux/systemd/Docker mechanics. They do not establish a confined Rocky production design.

## Follow-up experiment result

The follow-up began from guest state matching the `docker-installed` checkpoint. The automation identity could not independently query Proxmox snapshot lineage, so this is an observation rather than a hypervisor-verified fact.

Measured results:

1. `PASS`: Docker reports SELinux among its security options.
2. `PASS`: normal test containers receive non-empty process and mount labels and run as `container_t`, not `spc_t`.
3. `NOT RUN`: the helper still needs a dedicated test domain; it currently runs as `unconfined_service_t`.
4. `NOT RUN`: helper state and runtime paths still use generic `var_lib_t` and `var_run_t` types.
5. `PASS` for the semantic and DAC boundaries; `NOT RUN` for a dedicated SELinux helper-domain boundary.
6. `PASS`: no AVC was generated, and no allow rule was added.
7. `PASS`: Enforcing mode, firewalld, the corrected helper run, stopped-member rejection, Docker outage/recovery, and cleanup checks succeeded.

The narrow remediation was a validated `/etc/docker/daemon.json` containing `{"selinux-enabled": true}`. Docker's RHEL-compatible packages had already installed and loaded `container-selinux`; they did not enable daemon integration. The development installer now creates this minimal configuration only when the RHEL-family host has SELinux enabled and no daemon configuration already exists. It preserves existing configuration rather than attempting an unsafe shell-level JSON merge, then verifies the effective Docker security options on Enforcing hosts.

## Minimum future policy boundary

A production policy, if Rocky remains supported, should be narrower than the test's generic domains. Conceptually it needs to:

- transition the helper executable into a KITPro helper domain;
- assign dedicated types to the helper socket, runtime directory, state, audit data, and approved application-data roots;
- allow the helper domain to accept only the local Unix-socket traffic permitted by DAC and peer credentials;
- permit the exact Docker-socket interaction required by the Engine API boundary;
- permit only the filesystem and system operations represented by approved semantic helper operations; and
- deny arbitrary host-file, device, network-administration, execution, and unrelated container-data access.

Docker socket access remains root-equivalent authority even when SELinux narrows other edges. A policy cannot make an unrestricted Docker API safe; the semantic helper validation remains the primary control.

## Open questions

- Which executable and service packaging should trigger the helper domain transition?
- How should KITPro merge or provision Docker daemon settings when an administrator already has a valid `daemon.json`?
- Which permissions are actually needed between the helper domain and `container_var_run_t`?
- Can the final systemd sandbox restore `RestrictSUIDSGID` or replace it without blocking `openat2` on systemd 257?
- Which policy and daemon settings belong in the Rocky packaging adapter rather than the shared installer?
- How will upgrades test policy compatibility and avoid silently reverting to `spc_t` containers?
- Does a Docker Engine upgrade preserve the enabled daemon setting and expected container labels?

No production SELinux policy was written in this milestone.
