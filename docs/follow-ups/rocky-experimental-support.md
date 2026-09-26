# Rocky Linux 10 experimental-support backlog

Rocky Linux 10 is a promotion-ready experimental KITPro host. It uses the
shared catalog and helper protocol through the Podman/Quadlet runtime adapter.
The old Docker experiment remains evidence for portability but is not the
target path. Support designation and RPM publication remain separate release
actions.

| Work | Current evidence | Completion requirement |
| --- | --- | --- |
| Clean native installation | `PASS` on Rocky Linux 10.2 with Podman 5.8.2, crun 1.27, Quadlet, firewalld, and SELinux Enforcing; Docker was absent | Keep the immutable [`2026-09-15 acceptance result`](../testing/results/2026-09-15-rocky10-podman-clean-host-acceptance.md) as the baseline and rerun it after runtime/package changes. |
| Reproducible RPM build | Spec, builder, and static tests exist | Build `kitpro-server` and `kitpro-selinux` twice on Rocky 10 and compare payloads, metadata, and checksums. |
| Dedicated helper SELinux domain | No helper-specific AVC occurred during the full native acceptance run; the current RPM needs only standard application-data contexts | Reconsider a narrow helper transition only if observed required operations demonstrate a policy need. Do not install speculative allow rules or a permissive domain. |
| Managed and imported storage labels | `PASS` for private managed `:Z`, exact imported-root labeling, read-only and read-write access, MCS isolation, UID/GID behavior, and restored managed labels | Validate NFS/CIFS on a separately available mount. |
| firewalld regression | `PASS` for private, exact loopback, exact LAN, reboot, and uninstall under Rocky's default `StrictForwardPorts=no` | Add generation-aware explicit forward rules before claiming compatibility with an administrator-enabled `StrictForwardPorts=yes`. |
| Quadlet recovery | `PASS` for DNS, Paperless dependency recovery, slow image/start behavior, container kill, daemon reload, and host reboot | Rerun after material Podman or Quadlet generator changes. |
| Rocky package upgrades | `PASS` for native alpha11 through alpha19 DNF upgrades, backups, migration, daemon reload, secrets, data, and health | Rerun after lifecycle-script or schema changes. |
| Remove/reinstall and purge | `PASS`; running state and data returned after reinstall, while acknowledged purge retained imported storage, Podman/images, firewalld, and unrelated SELinux policy | Rerun after lifecycle-script changes. |
| Full visible catalog | Historical alpha.12 evidence covered 15 applications. Alpha.13 has 20 visible applications, but normal installation stops before runtime creation because the Podman adapter lacks the staged-generation lifecycle contract. | Implement and qualify the Podman lifecycle contract before claiming alpha.13 application coverage. |
| Application-data backup and restore | `PASS`; Ollama, Open WebUI, and Paperless-ngx passed on Rocky/Podman, including generated secrets, SQLite, media, excluded Redis, restart, reboot, SELinux labels, and AVC review | Preserve the immutable [`2026-09-16 acceptance result`](../testing/results/2026-09-16-application-backup-restore-acceptance.md) and rerun after backup format or restore lifecycle changes. |
| x86-64-v3 documentation | The validation CPU passed | Document the requirement before users install Rocky. Provide a preflight check and explain that older AMD and Intel systems may be incompatible. |

The applicable technical promotion gates now pass. Keep the published product
label experimental until the support designation is approved and RPMs are
released. Record each later real-host run as a new immutable result.
