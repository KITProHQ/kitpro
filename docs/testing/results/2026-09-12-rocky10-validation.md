# Rocky Linux 10 platform validation: 2026-09-12

## Run identity

- Overall status: `FAIL` for primary-reference acceptance; core privilege-boundary result is otherwise substantially positive
- VM: Proxmox VM 501, `10.10.0.114`
- OS: Rocky Linux 10.2 (Red Quartz)
- Baseline snapshot: `clean-os`
- Post-install snapshot: `docker-installed`
- Additional disposable ownership checkpoint: `rocky-ownership-base`
- Repository baseline: `8e8e05176b5f9c2669d702b679e034828bdc08f4`
- Fixture run ID: `9dd2f301-36a2-4465-97ce-57f7f6771c90`
- Completed: `2026-09-12T17:30:44-07:00`
- Procedure: [`prototypes/privilege-boundary/RUNBOOK.md`](../../../prototypes/privilege-boundary/RUNBOOK.md)
- SELinux analysis: [`docs/testing/selinux-rocky.md`](../selinux-rocky.md)

The acceptance failure is specific: SELinux remained Enforcing, but Docker did not enable SELinux integration. The test container ran as `spc_t` with empty Docker process and mount labels, and the helper ran as `unconfined_service_t`. No broad workaround or enforcement change was used. This result does not disprove Rocky viability, but it prevents claiming that the tested default installation provides the intended mandatory-access-control boundary.

## Source and evidence identity

The installer and initial fixture came from commit `8e8e05176b5f9c2669d702b679e034828bdc08f4`. The source archive used on the guest had SHA-256 `6ba0a1b9fa613d69d522b32f10fd70493dfab63681764799d3fed26546c56e41`.

Real-host findings required five test-fixture corrections or follow-ups during the run:

1. remove `RestrictSUIDSGID=yes` from the disposable helper unit because systemd 257 on Rocky made the required `openat2` call return `ENOSYS`;
2. stop treating Docker 29's network-inspect member map as complete for created or stopped containers;
3. remove `Requires=docker.service` so a helper request reports `DockerUnavailable` instead of activating a deliberately stopped Docker daemon;
4. enumerate all containers by network, including stopped containers, before lifecycle or destructive operations so a stopped foreign endpoint cannot hide from policy; and
5. shorten the hardcoded BusyBox wait loop so its PID 1 shell handles TERM before Docker's forced-stop timeout.

The first three corrections passed the real helper workflow. The final stopped-member adapter and stop-command corrections passed targeted real Docker probes and the complete 35-test local suite; the full helper workflow was not redeployed after cleanup. These are prototype corrections, not production KITPro code or a production language decision.

Selected evidence logs were copied off the VM before cleanup:

| Evidence | SHA-256 |
| --- | --- |
| Docker installation transcript | `4250f98c7e1effaa53eaef86a1224a93a3845c3215854b01997b0f232be57003` |
| Real filesystem and mount test | `11221275bd4ab569f24794afff134882e3179bfb1734edc9488cd38b15d19b12` |
| Network and firewalld inspection | `577a8bea931affe645ade3ec456eba0f7de44da6aafe6481f0886b7ff247c9ec` |
| SELinux runtime contexts | `7a92d5592a4e9b62c1ceaca981cd60fbe2a710de7b5062fca9994ed313d14898` |
| Docker SELinux configuration | `9b6c98e305a697f80191ab9bd72a0d3d58e5da0b79243f650ef1b19654d1ba79` |
| Reboot recovery | `d3e061eb70a6db7ddb98c3d404eaefa31b85f2653d04408714b3a91741994d09` |
| Exact replay after reboot | `07badcc3c3510dde7dd9631703ed533d0571a2c96a31fa49df62bd43ea3b0391` |
| Resource cleanup | `03827d2fbf4854db3e054b45dbe6d413a21a5e30b611cd9f087714b01db5f6f4` |
| Stopped-network-member adapter probe | `ed2f1d8d9e19cb009d5c12c956bc97c119025da32d46d0b2ccb17cb0b662fe95` |

The test used multiple bounded command transcripts rather than one monolithic raw transcript. Earlier failed-attempt output is retained in this result as findings; it was not rewritten into a pass.

## Baseline observations

| Item | Status | Observation |
| --- | --- | --- |
| VM identity | PASS | VM 501 at `10.10.0.114` reports Rocky Linux 10.2. |
| Kernel and architecture | PASS | `6.12.0-211.54.1.el10_2.x86_64`, `x86_64`. |
| PID 1 and systemd | PASS | systemd PID 1, version 257. |
| cgroup mode | PASS | Unified cgroup v2. |
| CPU and memory | PASS | 2 vCPU, 4092 MiB; guest exposes AVX2, BMI2, FMA, MOVBE, and SSE4.2 required by the Rocky 10 x86-64-v3 baseline. |
| Root filesystem | PASS | XFS on `/dev/mapper/rlm-root`, approximately 34.1 GiB total and 31.9 GiB free. |
| Network | PASS | `ens18`, `10.10.0.114/24`, private default route via `10.10.0.1`. |
| MAC and firmware | OBSERVATION | `bc:24:11:00:81:f2`; legacy BIOS boot. |
| SELinux baseline | PASS | Targeted policy, `Enforcing`. |
| firewalld baseline | PASS | Active and enabled; `ens18` in the `public` zone. |
| Docker inventory | PASS | Clean before installation; no unrelated workload inventory was adopted. |
| QEMU guest agent | FAIL | Not running. Proxmox guest-agent graceful restart control was unavailable. A later in-guest reboot succeeded. |
| Journal persistence | OBSERVATION | Rocky's journal was volatile, so logs from an earlier boot were unavailable after reboot. |

## Docker installation and authority

| Test | Status | Evidence |
| --- | --- | --- |
| KITPro installer | PASS | Detected Rocky 10 and visibly used Docker's RHEL-compatible repository with the derivative-support caveat. No manual package workaround was used. |
| Signing key and packages | PASS | Docker repository key verified. Installed Engine, CLI, containerd, Buildx, and Compose plugin. |
| Versions | PASS | Docker Engine/CLI 29.8.0, API 1.56, containerd 2.3.5, runc 1.5.1, Compose 5.5.1, Buildx 0.37.1. |
| Runtime status | PASS | Docker and containerd active; Docker answered version and info requests. |
| Docker runtime mode | PASS | cgroup v2 with systemd cgroup driver; overlayfs storage driver. |
| SELinux after installation | PASS | Still `Enforcing`; installer did not change SELinux or firewalld. |
| Root authority | PASS | Root/sudo communicated with Docker. |
| `josh` authority | PASS | Direct Docker request denied before any group change. `josh` was not in `docker`; the group was empty. |
| API-test identity authority | PASS | UID 993, GID 992, not in `docker`, `wheel`, or another privileged group; direct Docker socket request denied. |
| Docker socket | PASS | `root:docker`, mode 0660, SELinux type `container_var_run_t`. |
| Docker-group escalation | OBSERVATION | Not performed. The architecture and installer correctly treat group membership as root-equivalent authority. |
| Snapshot | PASS | `docker-installed` created only after installer verification and Enforcing-mode recheck. |

## Privilege and protocol boundary

| Test | Status | Evidence |
| --- | --- | --- |
| Runtime directory | PASS | `root:kitpro-pb-api-test`, mode 0750. |
| Helper socket | PASS | `root:kitpro-pb-api-test`, mode 0660. |
| Socket activation | PASS | Socket active at boot; helper inactive until a request and then activated. |
| Allowed identity and `SO_PEERCRED` | PASS | Helper observed UID 993. Response explicitly reported `human_authorization_verified: false`. |
| Wrong peer | PASS | Root could reach the socket's DAC boundary but helper rejected root's peer UID. |
| Unrelated user | PASS | `nobody` could access neither Docker nor the helper socket. |
| Helper-to-Docker | PASS | Helper inspected Docker Engine API 1.56 while the API identity remained denied direct access. |
| Typed operation vocabulary | PASS | Protocol offered only the disposable semantic operation set; no generic Docker, Compose, file-write, or command tunnel existed. |
| Malformed, oversized, stale, and unknown input | PASS | Rejected before Docker dispatch; helper remained available. |
| Concurrent/disconnected clients | PASS | Repository suite passed on the guest; no crash or privilege expansion observed. |
| Audit evidence | PASS | Structured helper events recorded operation kind, operation ID, peer UID, completion/failure, and error code without secrets. |

## Docker resource and ownership behavior

| Test | Status | Evidence |
| --- | --- | --- |
| Image identity | PASS | Discovery tag `docker.io/library/busybox:1.37.0`; deployed `docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0`; `linux/amd64`. |
| Safe container shape | PASS | UID 65534, read-only root filesystem, all capabilities dropped, `no-new-privileges`, no devices, binds, published ports, or host namespaces. |
| Lifecycle | PASS with observation | Create, inspect, start, bounded logs, stop, restart, and remove worked through the helper. The original long-sleep workload needed Docker's forced stop and exited 137. A targeted real-Docker probe of the corrected one-second loop stopped in 2.319 seconds with exit code 0. Full helper redeployment with that command is NOT RUN. |
| Exact record and labels | PASS | Helper required matching state, run ID, labels, name, kind, image, network, and Docker object identity. |
| Unrelated container | PASS | Helper rejected it and left it unchanged; it was removed later only by its exact disposable control name. |
| Missing labels | PASS | `OwnershipUnproven`; no mutation. |
| Missing helper record | PASS | `OwnershipUnproven`; no mutation. |
| Wrong instance or kind | PASS | Rejected; managed object unchanged. |
| Unknown Docker object | PASS | Rejected; no other object touched. |
| Label-only object | PASS | `OwnershipConflict`; no adoption or deletion. |
| Record-only object | PASS | `OwnershipUnproven`; unrelated control remained intact. |
| Changed image identity | PASS | `OwnershipUnproven`; no mutation. |
| Unexpected network | PASS | `PolicyDenied`; no mutation. |
| Running foreign network member | PASS | `PolicyDenied`; the disposable foreign member was removed manually and no unknown resource was altered. |
| Stopped foreign network member | PASS for adapter; NOT RUN end to end | Docker's all-container network filter returned both created/stopped endpoints while network inspection returned none. The updated adapter detected both. The full helper was not redeployed after cleanup. |
| Unknown volume manipulation | PASS | The protocol has no arbitrary volume-ID operation; no Docker volume was created. |

## Dangerous-configuration rejection

All of the following were rejected before Docker execution: privileged mode; host network, PID, and IPC namespaces; Docker-socket mounts; bind mounts for `/`, `/etc`, `/proc`, `/sys`, and `/dev`; arbitrary devices; arbitrary Linux capabilities; arbitrary security options; unrestricted sysctls; caller-provided Docker IDs; absolute host paths; raw Docker API forwarding; Compose/YAML execution; and arbitrary shell commands. Status: `PASS`.

## Filesystem safety

| Test | Status | Evidence |
| --- | --- | --- |
| Traversal and absolute paths | PASS | Rejected by the real Rocky/Python/kernel fixture. |
| Symlink escape and replacement | PASS | Both deterministic cases rejected. |
| Descriptor-relative containment | PASS | `openat2` operation created only the approved root-owned slot. |
| Unexpected mount/mount substitution | PASS | A disposable tmpfs mounted beneath the XFS test root was rejected as a cross-mount path. The mount was unmounted automatically. |
| Hard-link-sensitive file mutation | NOT RUN | The prototype exposes directory preparation, not privileged file replacement. No meaningful file hard-link operation exists to test. |
| High-frequency race harness | NOT RUN | The deterministic replacement case passed, but the fixture has no specialized rename/link/mount race harness. |

## Networking and firewalld

| Test | Status | Evidence |
| --- | --- | --- |
| Per-instance network | PASS | One user-defined internal bridge, `Attachable=false`, `Ingress=false`, IPv6 disabled. |
| Host/default network exclusion | PASS | Container used only the instance network; no host network or default bridge attachment. |
| Default port publication | PASS | Empty Docker port bindings and no Docker proxy/listener. |
| Unrelated networks | PASS | Built-in networks remained; no unrelated network was removed or adopted. |
| IPv4 loopback publication | NOT RUN | The disposable protocol deliberately has no published-port operation. |
| IPv6 loopback publication | NOT RUN | The protocol has no published-port operation and the instance network had IPv6 disabled. |
| External reachability | PASS for default/no-port case | No host listener existed. A second-host loopback-publication test was not applicable without a publication operation. |
| firewalld interaction | OBSERVATION | Docker placed `docker0` and the instance bridge in its `docker` zone and installed the `docker-forwarding` policy plus iptables-nft chains. The external NIC remained in `public`. Docker's internal-network chains dropped traffic crossing outside `172.18.0.0/16`. |

No firewall rule was weakened or edited by KITPro tooling.

## SELinux evidence

| Test | Status | Evidence |
| --- | --- | --- |
| Enforcing throughout | PASS | Baseline, post-install, fixture, restart, reboot, and cleanup checks all returned `Enforcing`. |
| API client context | OBSERVATION | `unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023`. |
| Helper context | FAIL for intended production posture | `system_u:system_r:unconfined_service_t:s0`. |
| Runtime/socket/state contexts | OBSERVATION | Runtime and helper socket `var_run_t`; state `var_lib_t`; Docker socket `container_var_run_t`. |
| Docker daemon | OBSERVATION | `container_runtime_t`; `container-selinux` 2.246.0 installed. |
| Container context | FAIL | Host process ran as `spc_t`; Docker reported empty `ProcessLabel` and `MountLabel`. |
| Docker security options | FAIL | Only built-in seccomp and cgroup namespaces reported; `name=selinux` absent. `/etc/docker/daemon.json` was absent and the packaged unit did not pass a SELinux-enable flag. |
| AVC review | PASS for unexplained denials | Audit service active; `ausearch` returned no AVC or USER_AVC matches during the fixture window. This does not compensate for unconfined domains. |
| Security compromise | PASS | No enforcement disablement, permissive domain, `audit2allow`, broad policy, firewalld weakening, or Docker-group grant was used. |

## Restart, recovery, and cleanup

| Test | Status | Evidence |
| --- | --- | --- |
| Client reconnect | PASS | New processes resumed requests through the socket. |
| Exact operation replay | PASS | Same UUID/body replayed the stored result across helper restart and full VM reboot. |
| Conflicting UUID reuse | PASS | `OperationConflict`. |
| Unknown/stale operation | PASS | Bounded `NotFound`/stale rejection. |
| Helper restart | PASS | Root-owned receipt and ownership state survived. |
| Docker unavailable | PASS after unit correction | Helper returned retryable `DockerUnavailable` and did not activate Docker. |
| Docker recovery | PASS | Explicit Docker start restored Engine inspection. |
| Persisted `running` receipt | PASS in guest repository test | On restart it becomes `RecoveryRequired`; no automatic reconciliation is claimed. |
| Arbitrarily interrupted real Docker mutation | NOT RUN | No deterministic interruption harness exists. |
| Full VM reboot | PASS | Boot ID changed; Docker/socket returned; helper activated on request; modes and state checksums were preserved. |
| Graceful hypervisor guest-agent reboot | FAIL | QEMU guest agent unavailable; this control path timed out. In-guest `systemctl reboot` succeeded. |
| Cleanup | PASS | Helper removed managed container/network, unrelated control survived until exact manual removal, all fixed fixture paths/identity were removed, and Docker ended with zero containers/volumes and only built-in networks. |

## Platform assessment

Rocky Linux 10 is technically capable of running the portable Unix-socket helper and direct Docker Engine API boundary. The ownership model, fail-closed destructive checks, filesystem containment, systemd recovery, cgroup v2, Docker API, and firewalld coexistence all produced useful positive evidence.

It is not accepted as the Phase 1 primary reference from this run. Before that recommendation, KITPro must separately validate:

- Docker with SELinux integration enabled and normal confined container labels;
- a narrow packaged helper domain and persistent/runtime file types;
- the minimal allow rule, if any, for helper access to Docker's socket;
- the impact of restoring `RestrictSUIDSGID` or an equivalent hardening control without blocking required syscalls;
- one end-to-end rerun of the final all-container membership and graceful-stop corrections;
- persistent journaling and a functioning guest agent in the reference VM recipe; and
- the still-blocked comparable Debian run.

The Rocky 10 x86-64-v3 requirement worked on this host but remains a support limitation for older repurposed AMD/Intel machines.
