# Application backup and restore acceptance, 2026-09-16

## Decision

- Implementation status: `READY`
- Rocky recommendation: `READY_TO_PROMOTE`
- Rocky host: disposable Rocky Linux 10.2 VM 501 at `10.10.0.114`
- Final installed acceptance build: `0.1.0~alpha19-1.el10`
- RPM source revision: `93efa3d88945a7027cf78f9998dcf4077f6f49ea`
- Backup format: `kitpro-application-backup`, version 1

The application backup and restore blocker recorded in the
[Rocky clean-host acceptance](2026-09-15-rocky10-podman-clean-host-acceptance.md)
is resolved. Representative filesystem, SQLite, generated-secret, imported
storage, and multi-container state passed backup and restore. Rocky remained on
native Podman and Quadlet with SELinux Enforcing and firewalld enabled. Docker
parity passed on the local Docker host without starting another Proxmox guest.

Rocky is still labeled experimental in product documentation because changing
the published support designation and publishing RPMs are separate release
actions. The technical recommendation from this acceptance run is
`READY_TO_PROMOTE`.

## Runtime and package evidence

| Item | Accepted value |
| --- | --- |
| Rocky | 10.2 (Red Quartz) |
| Podman | 5.8.2 |
| OCI runtime | crun 1.27 |
| systemd | 257 (`257-23.el10_2.2.rocky.0.1-gb237c67`) |
| SELinux | Enforcing; `selinux-policy-targeted-42.1.18-4.el10_2.3` |
| Container policy | `container-selinux-2.246.0-1.el10` |
| firewalld | `2.4.3-4.el10_2`, active and enabled |
| Storage | rootful overlay |
| Cgroups | v2 |
| Networking | netavark |
| Orchestration | system Quadlet units generated from `/etc/containers/systemd` |
| Docker on Rocky | Absent; executable and socket absent |
| Podman socket | Absent; helper used the bounded command adapter |
| Docker parity host | Docker Engine 29.7.2 |

Only VM 501 was running during the Rocky work. The Debian, Ubuntu, and Arch test
VMs remained stopped to avoid adding load to the Proxmox host.

The final RPMs were built natively on Rocky with:

```sh
./software/server/packaging/build-rpm.sh 0.1.0~alpha19 /tmp/kitpro-rpm-alpha19
```

| Artifact | Size | SHA-256 |
| --- | ---: | --- |
| `kitpro-server-0.1.0~alpha19-1.el10.x86_64.rpm` | 23,731,466 bytes | `8ba4f515bdc4067efd37ed396c22ad900457c8d66b8172de7e7f6cc021178b4b` |
| `kitpro-selinux-0.1.0~alpha19-1.el10.x86_64.rpm` | 10,699 bytes | `57661d27eef13e3e34a5718335251d86449505887d5762db09d28fc9c2555081` |
| `kitpro-server-0.1.0~alpha19-1.el10.src.rpm` | 1,506,625 bytes | `9d4dbfcf754d47fe39c951e1cea2af69ff453058e3b8df1b2527eb59821e292f` |

`rpm -K` returned `digests OK` for all three local, unsigned artifacts. DNF
upgraded alpha17 to alpha18 and then alpha18 to alpha19, creating and verifying
schema-8 control/helper backups each time. No artifact was published.

## Backup architecture accepted

The archive describes application state and contains no Docker or Podman
storage layout. Format version 1 contains a strict manifest, installation
metadata, included managed storage, generated secrets, and a SHA-256 index
under one `kitpro-backup/` root.

| Area | Accepted behavior |
| --- | --- |
| Runtime boundary | The helper uses the normalized installation plan and the shared container runtime interface. The archive is identical in principle for Docker and Podman. |
| `metadata-only` | Records stateless installation identity without stopping the application. |
| `cold-filesystem` | Stops the running component set, stages included managed bind storage, creates the archive, and resumes the prior running set. |
| `cold-sqlite-filesystem` | Uses the same cold consistency point, detects SQLite headers, and runs integrity and foreign-key checks on staged databases. |
| Filesystem | Includes only catalog-declared managed paths below `/srv/kitpro/apps`; ownership, modes, and modification times are preserved. |
| Secrets | Includes only generated secrets declared by the trusted catalog. Archives are root-owned mode 0600 in a root-only 0700 directory. Secret values are not returned by the API or logged. |
| Imported storage | Records the trusted binding and `content_included: false`; external content is not copied or relabeled. |
| Database engines | SQLite is handled. PostgreSQL and MariaDB/MySQL are intentionally unsupported because no visible catalog plan deploys them. Redis is excluded when it is only the Paperless broker/cache. |
| Destination | Local administrator-controlled storage at `/var/lib/kitpro-helper/application-backups`; cloud and NAS destination adapters are not implemented. |
| Encryption | Not defined by format version 1. Operators must use established encrypted storage or authenticated encryption when moving an archive off-host. |

## Restore architecture accepted

Restore checks the full archive hash, format, bounded extraction limits, every
payload checksum, installation and catalog identity, release, runtime
generation, component image digests, storage topology, imported bindings, and
SQLite inventory before it changes live storage.

The helper stages a new tree beside the live tree, stops the previously running
components, swaps managed paths to rollback names, restores generated secrets
in one database transaction, and resumes only the components that were running.
Rollback restores the prior files and secrets if activation or restart fails.
The failed restored tree is retained with a bounded operation suffix for
diagnosis.

On Rocky, restored managed paths inherited or regained `container_file_t` and
private MCS categories through the established managed-path and `:Z` model.
Imported paths were not relabeled. No SELinux boolean, permissive domain,
privileged container, or additional helper capability was added.

## Representative live results

### Ollama: filesystem strategy on Podman

- Installation: `inst-4e49e79ee780bc8a`
- Backup: `op-9db6b4ef235080f53eebd197678f5b74`
- Archive SHA-256: `a0537776...`
- Result: a deterministic model-directory marker was changed after backup and
  returned byte-for-byte after restore.
- SELinux: restored content had `container_file_t`; no relevant AVC occurred.

### Open WebUI: SQLite, files, and generated secret on Podman

- Installation: `inst-f2086961ab28de89`
- Backup: `op-528d5a93b5d1d3ec4d3cc6feacdc7466`
- Archive SHA-256: `50b5fb...`
- Result: a deterministic SQLite row returned after mutation; `PRAGMA
  integrity_check` returned `ok`; the archived and restored `WEBUI_SECRET_KEY`
  hashes matched without exposing the value.
- Health: root and health endpoints returned HTTP 200 after runtime recreation.
- SELinux: restored content had `container_file_t`; no relevant AVC occurred.

### Paperless-ngx: multi-container SQLite and media on Podman

- Installation: `inst-3b17ea8847307f4f`
- Backup: `op-fba73e9bacb9458918064ce164526829`
- Archive SHA-256:
  `525dfc44cd37482e5d4ecb4d510bdb9a89e1fd6c0cc4cf7617e059af302a397d`
- Database: the mutated record returned to `paperless-before`; SQLite integrity
  returned `ok`.
- Media: the restored marker returned to SHA-256
  `d7a9838c4b6fd8dc342ca6e1258e54f0a543eb9f03802c5a365bd5ea505f1ade`.
- Redis: the excluded broker marker retained its post-backup SHA-256
  `18e8588bf66f3346b2860710b8878700a948e564e5947f415b88aafc89d3107a`.
- Runtime: broker and web Quadlet services returned active; the web component
  resolved and connected to `broker`; Celery reported ready.
- HTTP: exact loopback exposure at `127.0.0.1:20001` returned the expected 302
  authentication redirect.
- Cleanup: alpha19 left no new restore staging, rollback, or workspace tree.

### SFTPGo: live Docker parity and imported storage

The opt-in integration test used the catalog-pinned SFTPGo digest on Docker
Engine 29.7.2. It created a temporary Docker network, container, managed config
tree, and imported read-write tree. Backup stopped and restarted the real
container. Restore returned a mutated SQLite row to its original value and
restarted the container. The imported marker retained its post-backup value,
proving reference-only semantics. Test cleanup removed the temporary container
and network.

## Reboot and SELinux result

Rocky rebooted after the successful Paperless restore and exposure recreation.
The API, helper socket, firewalld, generation-2 broker and web Quadlet services,
and Podman network returned automatically. The restored database row, database
integrity, media hash, excluded Redis hash, and loopback HTTP response all
persisted. Docker and Podman API sockets remained absent.

`getenforce` returned `Enforcing`. Managed Paperless paths had
`container_file_t` with a shared private MCS pair for the web component. The
full acceptance-window AVC query and the post-boot AVC query found no relevant
KITPro or container denial.

## Failure tests

| Scenario | Result |
| --- | --- |
| Insufficient destination space | PASS: rejected before runtime quiescence |
| Interrupted archive creation | PASS: `.partial` artifact removed |
| Invalid archive/path traversal | PASS: rejected; extraction destination removed |
| Unsafe symlink or special file | PASS: rejected |
| Complete-archive hash mismatch | PASS: rejected before extraction |
| Payload checksum mismatch | PASS: rejected |
| Unsupported format version | PASS: rejected |
| Wrong installation/application | PASS: rejected |
| Missing/mismatched topology | PASS: strict manifest validation rejected it |
| SQLite integrity or foreign-key failure | PASS: rejected before activation |
| Application restart failure | PASS: prior data and runtime restored; failed restored tree retained |
| Read-only/application-owned cleanup | PASS: workspace and rollback cleanup regression tests plus live alpha19 proof |
| Interrupted PostgreSQL/MariaDB dump or failed import | Not applicable: those strategies are not implemented or admitted by the current catalog |

## Visible catalog strategy coverage

All 15 visible schema-version-6 manifests declare a trusted typed strategy.
"Strategy coverage" means schema and shared strategy tests pass; it does not
claim an individual live data round trip for that application.

| Application | Strategy | Validation |
| --- | --- | --- |
| Actual Budget | `cold-sqlite-filesystem` | Strategy coverage |
| Audiobookshelf | `cold-sqlite-filesystem` | Strategy coverage |
| FreshRSS | `cold-sqlite-filesystem` | Strategy coverage |
| Home Assistant | `cold-sqlite-filesystem` | Strategy coverage |
| IT-Tools | `metadata-only` | Direct automated no-downtime coverage |
| Jellyfin | `cold-sqlite-filesystem` with cache excluded | Strategy coverage |
| Mealie | `cold-sqlite-filesystem` | Strategy coverage |
| Memos | `cold-sqlite-filesystem` | Strategy coverage |
| Navidrome | `cold-sqlite-filesystem`; imported music reference-only | Strategy coverage |
| Ollama | `cold-filesystem` | PASS: live Rocky/Podman |
| Open WebUI | `cold-sqlite-filesystem` | PASS: live Rocky/Podman, including generated secret |
| Paperless-ngx | `cold-sqlite-filesystem`; Redis excluded | PASS: live Rocky/Podman multi-container |
| SFTPGo | `cold-sqlite-filesystem`; imported files reference-only | PASS: live Docker |
| Uptime Kuma | `cold-sqlite-filesystem` | Strategy coverage |
| Vaultwarden | `cold-sqlite-filesystem` | Strategy coverage |

The hidden BusyBox lifecycle fixture exposed an unrelated missing-command test
fixture defect and was not used as public catalog evidence.

## Required gates

| Gate | Result |
| --- | --- |
| Filesystem application backup/restore | PASS: Ollama on Rocky/Podman |
| SQLite backup/restore | PASS: Open WebUI on Rocky/Podman |
| Multi-container database backup/restore | PASS: Paperless-ngx on Rocky/Podman |
| Docker backend | PASS: live SFTPGo round trip |
| Podman backend | PASS: live Ollama, Open WebUI, and Paperless-ngx round trips |
| SELinux restore | PASS: Enforcing, correct managed labels, no relevant AVC |
| Reboot after restore | PASS |
| Checksum validation | PASS |
| Invalid archive rejection | PASS |
| Failure cleanup | PASS |
| Application health | PASS |
| Full visible catalog | PASS from the clean-host run; all manifests now also have validated backup policy |

## Defects found and corrected

| Commit | Classification | Defect and correction |
| --- | --- | --- |
| `5c81df8` | Filesystem/runtime-neutral | Backup staging applied application ownership before descendants were copied. Metadata is now applied leaf-first after copy. |
| `d07e78f` | Archive/runtime-neutral | Extraction applied directory ownership before children and failed without broad DAC privilege. Directory metadata is now applied leaf-first after checksum validation. |
| `93efa3d` | Cleanup/runtime-neutral | Ownership-preserved rollback trees could not be deleted by the least-privileged helper. Cleanup now reclaims only generated temporary trees with existing `CAP_CHOWN`; no capability was added. |

## Regression and package results

- `GOCACHE=/tmp/kitpro-go-cache go test ./...`: PASS
- `GOCACHE=/tmp/kitpro-go-cache go vet ./...`: PASS
- Backup and restore focused tests: PASS
- Live Docker opt-in test: PASS
- Debian package static test: PASS
- Arch package static test: PASS
- RPM package static test: PASS
- Debian artifact: `kitpro-server_0.1.0~alpha19_amd64.deb`, SHA-256
  `f8b769841cc08a37858c57641e7cf485ad2ebc358d6eef8fe5e24005b8dfa9f7`
- Arch artifact: `kitpro-server-0.1.0_alpha19-1-x86_64.pkg.tar.zst`,
  SHA-256
  `7129ac38aad44c6140535c7f238694fcf9a11a320370c4e9f0efc68dbb4b7f99`

The Arch build completed successfully. Its trailing `makepkg` version probe
printed a harmless broken-pipe warning after the package and metadata had been
created and checksummed.

## Remaining limitations

- Format version 1 restores only into the exact existing installation,
  release, generation, topology, and imported-binding identity.
- Archives are local and unencrypted. Scheduling, retention, object storage,
  bare-host recovery, and host-to-host restore are not implemented.
- Imported external content is not included. The administrator must protect it
  independently.
- PostgreSQL and MariaDB/MySQL strategies are not implemented. The current
  visible catalog does not deploy those engines.
- NFS/CIFS imported storage was unavailable for live Rocky testing.
- Rocky firewalld hosts using non-default `StrictForwardPorts=yes` still need
  explicit generation-aware forwarding integration.
- Rocky GPU support remains unavailable pending a separate CDI design and
  certification.
- The hidden BusyBox fixture needs its own durable command before it can serve
  as a live lifecycle fixture.

## Recommendation

`READY_TO_PROMOTE`

The application-aware backup and verified restore blocker is closed. All
applicable Rocky acceptance gates now pass with SELinux Enforcing, firewalld
enabled, native rootful Podman/Quadlet, no Docker dependency on Rocky, no
privileged containers, no added broad capability, and no unexplained AVC.
