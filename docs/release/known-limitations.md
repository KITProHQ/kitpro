# Known alpha limitations

Status date: 2026-09-16. These limits apply to the current public release, `v0.1.0-alpha.11`.

## Platform and runtime

- Only Debian 13 amd64, Ubuntu 26.04 LTS amd64, and fully updated Arch Linux x86_64 with `linux-lts`, rootful Docker, and enforcing AppArmor are certified.
- Rocky Linux 10 amd64 remains experimental in the published product. Its native Podman, Quadlet, SELinux, firewalld, RPM, application backup/restore, and recovery acceptance passed in development source; support designation and a published RPM remain separate release actions.
- The catalog is intentionally limited. Arbitrary Compose, shell commands, capabilities, privileged mode, host networking, devices, bind mounts, and user-supplied manifests are not accepted.
- LAN exposure binds one configured host address. Wildcard/public-Internet exposure, reverse proxies, domains, and TLS automation are outside the current scope.

## Hardware acceleration

- NVIDIA Ollama inference is validated on the supported Debian, Ubuntu, and Arch boundaries with an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0. Other driver, toolkit, and GPU combinations remain unvalidated.
- KITPro currently accepts one unambiguous GPU. Multi-GPU selection and scheduling are not implemented.
- KITPro grants bounded device access but does not schedule GPU work or reserve VRAM when applications share one GPU.
- AMD device scoping has limited evidence, but AMD compute workloads are not live-certified.
- Intel accelerated workloads are not live-certified.
- Jellyfin can request optional NVIDIA access, but NVENC/NVDEC transcoding is not certified.
- Arbitrary USB, input, TTY, KVM, disk, memory, and other host devices remain unsupported. There is no raw device escape hatch.

## Storage and recovery

- Jellyfin supports one administrator-approved read-only media root. Multiple libraries and write-enabled media changes are not supported.
- Read-write trusted roots are exclusive. KITPro does not merge concurrent writers or provide file-level locking.
- KITPro registers existing local or host-mounted NFS/CIFS storage; it does not mount network shares or manage NAS credentials.
- Application backup format version 1 protects catalog-declared managed files, detected SQLite databases, and generated secrets for exact existing-installation restore. This source behavior is not part of the current public `v0.1.0-alpha.11` release.
- Imported external data is reference-only. KITPro records the trusted binding but does not copy NAS, media, music, audiobook, or SFTPGo external file content.
- Backup destinations are local and archives are not encrypted by KITPro. Scheduling, retention, object storage, bare-host recovery, and host-to-host restore are not implemented.
- PostgreSQL and MariaDB backup strategies are not implemented. No current visible catalog application deploys either database engine.
- Rollback of irreversible upstream schema migrations is not promised.

## Application and network boundaries

- Open WebUI and Ollama are separate installations. Cross-installation trusted service discovery is not implemented, and KITPro does not inject an unvalidated backend URL.
- Immich is not supported. Its current production topology requires shared component secrets, health-gated dependencies, bounded database shared memory, atomic multi-component updates, and database-aware backup/rollback that are not current trusted primitives.
- Syncthing is not supported. Typed UDP publication alone does not resolve upstream's LAN-discovery limitation under Docker bridge networking, and KITPro prohibits host networking.
- The archived original File Browser is not supported. SFTPGo is the maintained, first-run-authenticated read-write catalog choice.
- LocalAI is excluded because its official image can fetch an unsigned mutable backend during model installation; pinning only the outer image does not provide end-to-end provenance.
- Clustering, high availability, and automatic failover are not implemented.

## Update boundary

- Application updates are trusted-catalog and administrator initiated.
- Native package managers remain authoritative for KITPro package updates.
- Pre-update backups protect bounded control-plane state, not imported external data or the complete host.
