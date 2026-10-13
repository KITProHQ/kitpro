# Rocky Linux 10.2 Podman clean-host acceptance, 2026-09-15

## Decision

- Implementation status: `PARTIALLY_READY`
- Support recommendation: `REMAIN_EXPERIMENTAL`
- Acceptance host: Proxmox VM 501, `10.10.0.114`, restored from the disposable
  `clean-os` snapshot
- Started: `2026-09-15T21:00:50-07:00`
- Completed: `2026-09-15T23:45:00-07:00`
- RPM source revision: `049968d001f6ae16b267262a2ef069fa76b91854`
- Checklist: [`docs/testing/rocky-linux-10-validation.md`](../rocky-linux-10-validation.md)

The native Rocky runtime, package lifecycle, representative applications, and
all 15 visible catalog applications passed on a clean Rocky Linux 10.2 host.
SELinux remained Enforcing, firewalld remained enabled, Docker was absent, and
no Podman API socket or permissive workaround was used.

Rocky must remain experimental because KITPro does not yet provide a complete
application-data backup and restore workflow. The bounded control/helper SQLite
backup passed, but that backup does not contain application databases, uploads,
or complete application configuration. There is no corresponding production
restore operation. This is an explicit current product limitation, not a Rocky
or Podman-specific failure.

## Clean-host evidence

The baseline was captured before package installation or application mutation.
The host had no state from the earlier Docker experiment.

| Item | Status | Evidence |
| --- | --- | --- |
| Distribution | PASS | Rocky Linux 10.2 (Red Quartz), kernel `6.12.0-211.54.1.el10_2.x86_64`, x86-64-v3 |
| VM resources | PASS with capacity warning | 2 vCPU, 4092 MiB RAM, XFS root filesystem of about 34 GiB; approximately 32 GiB was initially free. The validator warned that RAM and disk were below its recommended production sizing. |
| PID 1 and cgroups | PASS | systemd 257, unified cgroup v2 |
| SELinux | PASS | Targeted policy 33, `Enforcing` before installation |
| firewalld | PASS | Active and enabled; `ens18` was in the default `public` zone with only `cockpit`, `dhcpv6-client`, and `ssh` services and no explicit ports |
| Docker | PASS | No Docker executable, RPM, service, socket, repository, or container state |
| Podman | PASS | Not installed before the native installer path ran; no pre-existing containers, networks, or volumes |
| KITPro | PASS | No package, service, runtime configuration, data, or application state |

VMs 500 (Debian), 502 (Ubuntu), and 503 (Arch) were stopped during the run after
the Proxmox host reached severe I/O contention. VM 501 remained running. The
three other guests did not contribute acceptance evidence.

## RPM build and artifacts

The RPMs were built natively on Rocky Linux 10.2 with:

```sh
./software/server/packaging/build-rpm.sh 0.1.0~alpha15
```

The build used RPM 4.19.1.1, Go 1.26.7, `selinux-policy-devel` 42.1.18, and the
Rocky systemd RPM macros. The SELinux module compiled successfully. The policy
contains a persistent file-context declaration for `/srv/kitpro/apps` and no
allow rules. The upstream policy compiler emitted duplicate interface warnings
for installed `passt` and `smartmon` interfaces; compilation and package
installation completed.

| Artifact | RPM metadata | SHA-256 |
| --- | --- | --- |
| `kitpro-server-0.1.0~alpha15-1.el10.x86_64.rpm` | 23,354,370 bytes; Apache-2.0 | `e60dd9a37f5a166d92f1dffe59c384e8854e7f0b9b89dc3b954e777fb4947545` |
| `kitpro-selinux-0.1.0~alpha15-1.el10.x86_64.rpm` | 10,699 bytes; Apache-2.0 | `4bb1e4c1cdea6d84ee58d77eb6429b6d2edb323b7b6ed483d6a43cbf2878cc72` |
| `kitpro-server-0.1.0~alpha15-1.el10.src.rpm` | 1,463,626 bytes; Apache-2.0 | `56632480acb705f27dee9b62b483c5d4bda356dadd4d7932a942400e3b83ca3e` |

All three artifacts returned `digests OK` from `rpm -K`. They are unsigned
local acceptance artifacts and were not published. DNF resolved the server
dependencies from Rocky repositories, including Podman 5, crun,
container-selinux, firewalld, systemd, and policycoreutils. No Docker repository
was added.

## Platform and runtime

| Component | Accepted value |
| --- | --- |
| Rocky | 10.2 (Red Quartz) |
| Podman | 5.8.2 (`podman-7:5.8.2-5.el10_2.x86_64`) |
| OCI runtime | crun 1.27 (`crun-1.27-2.el10_2.x86_64`) |
| systemd | 257 (`257-23.el10_2.2.rocky.0.1-gb237c67`) |
| SELinux policy | `selinux-policy-targeted-42.1.18-4.el10_2.3` |
| Container policy | `container-selinux-2.246.0-1.el10` |
| firewalld | 2.4.3-4.el10_2 |
| Cgroups | v2, systemd cgroup manager |
| Storage | rootful overlay, XFS backing filesystem, crun |
| Networking | netavark, per-installation Podman bridge networks |
| Quadlet | Podman 5.8.2 system generator, system units generated from `/etc/containers/systemd` |

Rootful Podman system services are the accepted model. It matches the managed
server lifecycle, boot ordering, exact host publication, backup ownership, and
administrative recovery requirements. Application containers were still
unprivileged in the OCI sense: no container used `--privileged`, all used
`no-new-privileges`, no broad capability addition was introduced, and explicit
non-root manifests ran with their declared UID/GID.

## Architecture exercised

| Compose-era concept | Rocky implementation and result |
| --- | --- |
| Application service | One generated `.container` Quadlet and normal systemd service per component |
| Application network | One generated `.network` Quadlet per installation; netavark DNS resolved component aliases |
| Persistent volume | Runtime-independent bind directories below `/srv/kitpro/apps`; no Podman named volumes were required |
| Environment and secrets | Root-owned files below `/etc/kitpro-server/runtime`, mode 0600; secrets were not placed in process arguments or Quadlet source |
| Dependencies | Quadlet/systemd `Requires=` and `After=` edges plus application readiness; Paperless web required its broker |
| Restart | systemd/Quadlet restart policy; killing Uptime Kuma recreated it without changing data |
| Published service | Exact loopback or exact LAN address and port; private services published no host port |
| Host control plane | Native `kitpro-api.service`, socket-activated helper, and typed helper protocol; no Docker or Podman socket |

The lifecycle checkpoint contained four application containers across three
installed applications, three Podman networks, seven generated Quadlet units,
six protected runtime metadata/environment files, `kitpro-api.service`,
`kitpro-helper.socket`, and the socket-activated helper service. Paperless was
the two-container application. The catalog as a whole exposed 15 visible
applications; the hidden BusyBox fixture was not used to determine catalog
acceptance.

## Installation and control plane

| Test | Status | Evidence |
| --- | --- | --- |
| Fresh native installation | PASS | DNF installed the two KITPro RPMs and Rocky dependencies; schema migration reached version 7 |
| API | PASS | `kitpro-api.service` active/enabled; authenticated version and application API requests returned 200 |
| Authentication | PASS | Initial administrator setup returned 303, login returned 303, authenticated home/API requests returned 200 |
| Helper | PASS | `kitpro-helper.socket` active/enabled; the service activated on a typed request |
| Socket isolation | PASS | Docker socket absent; Podman socket absent, inactive, and disabled; neither was mounted into a container |
| Quadlet generation | PASS | `daemon-reload` generated normal services; `systemctl`, `journalctl`, and Podman inventory agreed |
| Secrets and modes | PASS after fix | Runtime environment files were 0600. Upgrade backups were initially 0644; commit `29e4e41` made new backups 0600 and remediated retained backups during package upgrade. |

## Representative application gate

| Application | Shape | Status | Evidence |
| --- | --- | --- | --- |
| IT-Tools | Simple single container | PASS | Pinned digest, generated network/container units, active service, UI response, and clean removal |
| Uptime Kuma | Persistent single container | PASS | Pinned digest `92fd01c4...`, exact LAN publication `10.10.0.114:20001`, database inode and SHA-256 `ebf61b55...` preserved across reboot, crash, upgrade, remove/reinstall |
| Paperless-ngx | Multi-container | PASS | Pinned Paperless and Redis digests, web plus broker units, component DNS `broker.dns.podman`, Redis `PONG`, slow initial readiness, exact loopback publication `127.0.0.1:20002`, dependency stop/restart recovery |

The representative gate passed before the remaining visible catalog was
tested.

## Full visible catalog

| Application | Status | Rocky-specific result |
| --- | --- | --- |
| Actual Budget | PASS | One pinned, unprivileged container; labeled managed data; removal preserved data |
| Audiobookshelf | PASS after fix | Initial run failed because UID 1000 could not bind port 80. Commit `c76d0a4` set the application to port 13378; non-root startup, imported read-only media, and removal then passed. |
| FreshRSS | PASS | One pinned container; labeled data/extensions; removal preserved data |
| Home Assistant | PASS after fix | Initial clean pull exceeded the adapter's two-minute caller timeout. Commit `049968d` aligned the adapter with Quadlet's 30-minute start budget; a roughly 140-second clean pull then passed. DHCP packet discovery logged `Operation not permitted` under the least-privilege bridge model; no raw capability was granted. |
| IT-Tools | PASS | Representative simple application |
| Jellyfin | PASS | CPU-only path, no device mappings, managed config/cache with private labels, imported media read-only |
| Mealie | PASS | Initial migrations completed, UI became ready, persistent data retained |
| Memos | PASS | SQLite/WAL persisted with UID 10001 ownership |
| Navidrome | PASS | Imported media read-only; write attempt denied; database persisted |
| Ollama | PASS | CPU-only startup after a large pinned image pull; no GPU device or legacy passthrough |
| Open WebUI | PASS | Pinned digest, HTTP health 200, SQLite/cache under a private MCS label; runtime removal preserved data |
| Paperless-ngx | PASS | Representative multi-container application |
| SFTPGo | PASS | UID/GID 1000 wrote to the explicitly approved read-write imported root; host file remained 1000:1000 and removal preserved it |
| Uptime Kuma | PASS | Representative persistent application |
| Vaultwarden | PASS | Health and UI returned 200; SQLite data received a private MCS label and survived runtime removal |

No separate Rocky catalog or Rocky-only manifest fork was created. GPU support
was not tested and remains out of scope.

## Storage and SELinux

| Test | Status | Evidence |
| --- | --- | --- |
| Managed application storage | PASS | Directories below `/srv/kitpro/apps` received `container_file_t`; private writable mounts received distinct MCS categories through `:Z` |
| Managed ownership | PASS | Declared UIDs/GIDs were applied; Uptime, Paperless, Navidrome, Memos, SFTPGo, and other persistent applications wrote successfully |
| Imported read-only storage | PASS | An initially unlabeled `/mnt/kitpro-acceptance-music` root was inaccessible. An exact reviewed `semanage fcontext` rule plus `restorecon` enabled reads; container writes remained denied. KITPro did not relabel it automatically. |
| Imported read-write storage | PASS | Exact labeling and UID/GID 1000 allowed SFTPGo to create `kitpro-rocky-write-test.txt`; mode 0644, owner 1000:1000, and content hash survived uninstall and purge |
| NAS semantics | NOT RUN / not applicable | The disposable host had no NFS or CIFS mount. No NAS support claim is based on this run. |
| Custom policy | PASS | `kitpro-selinux` supplied only the `/srv/kitpro/apps` file context. No helper-domain allow rule, boolean, permissive domain, or blanket container permission was needed. |
| Enforcement | PASS | `getenforce` returned `Enforcing` before installation, throughout application testing, after reboot/upgrades, after uninstall/reinstall, and after purge |
| AVC audit | PASS | `ausearch -m AVC,USER_AVC` for the full acceptance window returned `<no matches>`; the kernel journal contained no SELinux denial. There are no denials to classify. |

The initial imported-root refusal did not produce an AVC even after disabling
dontaudit rules for the probe. It was treated as a fail-closed labeling case,
not as permission to weaken SELinux. Exact documented remediation was tested.

## Networking and firewalld

| Test | Status | Evidence |
| --- | --- | --- |
| Per-installation networks | PASS | Networks survived daemon reload and reboot; purge removed KITPro networks |
| Component DNS | PASS | Paperless web resolved `broker.dns.podman`; Redis answered `PONG` |
| Private mode | PASS | No host port was published |
| Loopback | PASS | Paperless returned 302 only at `127.0.0.1:20002` |
| LAN | PASS | Uptime Kuma returned 302 locally and from an authorized LAN peer at exact address `10.10.0.114:20001` |
| firewalld | PASS with documented boundary | firewalld stayed active/enabled. On Rocky's default `StrictForwardPorts=no`, netavark's exact published-port forwarding worked without adding a broad permanent port range. Podman bridge sources appeared in the trusted zone. No KITPro permanent public-zone rule was left after uninstall. |
| Strict forwarding mode | Known limitation | Hosts that opt into `StrictForwardPorts=yes` need generation-aware explicit forward rules; current guidance keeps services private/loopback unless that integration exists. This setting was not the clean Rocky default under test. |

## Reboot, failure, and package lifecycle

| Test | Status | Evidence |
| --- | --- | --- |
| Reboot recovery | PASS | API/helper, Quadlet services, networks, Uptime, Paperless, and Navidrome returned automatically; storage, secrets, and authentication persisted |
| Container crash | PASS | Killing Uptime Kuma caused systemd to create a new container ID within about 10 seconds; its database inode/hash and LAN endpoint were unchanged |
| Dependency failure | PASS | Stopping the Paperless broker stopped the required web unit; starting web brought the broker back, restored DNS, Redis readiness, and HTTP 302 |
| Control-plane restart | PASS | Package upgrades restarted API/helper without destroying application runtime state |
| Upgrade | PASS after fix | Native DNF upgrades alpha11 through alpha15 created integrity-checked backups, ran schema migration, applied daemon reload, preserved data/config/secrets, and restored application health. Alpha13 remediated all retained backup files to 0600. |
| Normal uninstall | PASS | `dnf remove kitpro-server` stopped containers, removed live Quadlets/runtime files, archived seven active units and six protected runtime files, and preserved control/helper state and all application data. Podman, firewalld, and `kitpro-selinux` remained. |
| Reinstall | PASS | Reinstalling alpha15 restored the exact archived units/files and running state. API/helper, Uptime LAN access, Paperless DNS/Redis/readiness, Navidrome, authentication, and data returned. |
| Purge | PASS | The acknowledged purge removed `/etc/kitpro-server`, `/srv/kitpro/apps`, both KITPro state trees, logs, Quadlets, containers, and networks. Removing the two KITPro RPMs removed the KITPro SELinux module. |
| Unrelated-state preservation | PASS | Imported roots and hashes, 16 cached OCI images, Podman, public-zone firewalld hash, and unrelated SELinux-module hash were unchanged after purge. Docker remained absent. |

## Backup and restore gate

| Test | Status | Evidence |
| --- | --- | --- |
| Control/helper backup | PASS | Authenticated backup operation created independent control and helper SQLite backups; integrity and foreign-key checks passed; files were 0600 after the acceptance fix |
| Application persistent data backup | FAIL | The backup contains bounded control-plane databases only. It does not capture application databases, uploads, files, or complete application configuration. |
| Restore | BLOCKED | There is no production application-data restore command or coordinated restore workflow to test. A package reinstall restores preserved runtime definitions, but it is not an application-data restore. |

This blocker agrees with
[`docs/release/known-limitations.md`](../../release/known-limitations.md), which
states that complete automatic application-data backup, host-to-host restore,
and disaster recovery are not implemented. Implementing consistent
application-aware snapshots and a separately authenticated restore operation
is not a narrow Rocky runtime repair, so it was not improvised during this
acceptance run.

## Required gates

| Gate | Result |
| --- | --- |
| Clean host preflight | PASS |
| RPM build | PASS |
| SELinux policy build/install | PASS |
| Fresh installation | PASS |
| API health | PASS |
| Authentication | PASS |
| Simple catalog application | PASS |
| Persistent-storage application | PASS |
| Multi-container application | PASS |
| DNS/readiness | PASS |
| LAN access | PASS |
| firewalld | PASS |
| Reboot recovery | PASS |
| Container failure recovery | PASS |
| Backup | FAIL — control state passes; application data is not backed up |
| Restore | BLOCKED — no complete application-data restore workflow |
| Upgrade | PASS |
| Uninstall | PASS |
| Reinstall | PASS |
| Purge | PASS |
| SELinux AVC review | PASS |
| Full visible catalog | PASS after the recorded fixes |

## Defects found and fixed

| Commit | Classification | Acceptance defect and correction |
| --- | --- | --- |
| `7138659` | Package/build | Support Rocky's native Go toolchain behavior |
| `6c448f6` | Package/build | Correct stripped Go binary assembly in RPM staging |
| `a939ed0` | Runtime/security | Isolate privileged Podman inventory execution |
| `8144d19` | Runtime/security | Permit only validated KITPro Podman container IDs |
| `efe9f97` | Runtime/systemd | Avoid piped `systemd-run` output in the helper boundary |
| `c1ff1fa` | Runtime/Quadlet | Render bind mounts in valid Quadlet form |
| `a93f755` | Runtime/catalog | Select the correct top-level release for multi-container plans |
| `69305b7` | Documentation | Record default firewalld forwarding behavior and exact imported-storage remediation |
| `29e4e41` | Security/package | Create and remediate backup files at mode 0600 across RPM, Debian, and Arch packaging |
| `c76d0a4` | Catalog/runtime-neutral | Move Audiobookshelf's non-root process from privileged port 80 to port 13378 |
| `049968d` | Runtime | Let clean-host Quadlet image pulls use the same bounded 30-minute start budget as systemd |

No fix introduced a Rocky-specific application fork. Shared changes were
runtime-neutral and retained Debian/Ubuntu/Arch behavior.

## Regression results

- `GOCACHE=/tmp/kitpro-go-test-cache go test ./...`: PASS
- `GOCACHE=/tmp/kitpro-go-vet-cache go vet ./...`: PASS
- Debian package static test: PASS
- Arch package static test: PASS
- RPM package static test: PASS

The first local Go invocation attempted to use the sandbox's read-only default
cache and failed before compilation. The isolated-cache rerun is the recorded
test result.

## Remaining limitations and recommendation

Promotion is blocked only by gates that are real product requirements, not by
Podman, Quadlet, systemd, SELinux, firewalld, packaging, application runtime,
or catalog compatibility:

1. complete application-data backup is not implemented;
2. complete application-data restore is therefore not testable;
3. NFS/CIFS imported-storage behavior was not exercised on this host;
4. firewalld `StrictForwardPorts=yes` lacks explicit generation-aware rules;
5. Home Assistant DHCP discovery is unavailable without a capability that this
   acceptance run correctly refused to add; and
6. Rocky GPU/CDI support remains out of scope and unavailable.

Final recommendation: `REMAIN_EXPERIMENTAL`. Rocky Linux 10.2 should move to
supported only after application-data backup and restore are implemented and
the complete applicable acceptance matrix is rerun successfully under SELinux
Enforcing with firewalld enabled and no Docker dependency.
