# Known alpha.13 limitations

Status date: 2026-09-26. These limits apply to the `v0.1.0-alpha.13`
release candidate.

## Platform and runtime

- Supported Docker hosts require an explicit, operator-selected RFC 1918
  default address pool with at least 64 available child networks of `/24` or
  larger. KITPro detects obvious current route and Docker-network conflicts but
  cannot predict future routes. It does not edit Docker daemon configuration.
  Docker must be restarted after an administrator changes that configuration,
  and the change applies only to newly created networks.
- Debian 13 amd64, Ubuntu 26.04 LTS amd64, and fully updated Arch Linux
  x86_64 with `linux-lts` are Supported with rootful Docker and enforcing
  AppArmor.
- Rocky Linux 10 amd64 and Podman remain Experimental. The native RPM,
  SELinux package, rootful Podman host checks, storage-root registration, and
  reboot recovery passed. Application installation is unavailable because the
  alpha.13 staged-generation lifecycle contract is not implemented by the
  Podman/Quadlet adapter. The failed request creates no runtime, network, or
  committed generation.
- The catalog is intentionally limited. Arbitrary Compose, shell commands, capabilities, privileged mode, host networking, devices, bind mounts, and user-supplied manifests are not accepted.
- LAN exposure binds one configured host address. Wildcard or direct
  public-Internet exposure is outside the current scope. KITPro does not
  provide a reverse proxy, Certbot or ACME certificate management, domain
  automation, or Cloudflare Tunnel integration.

## Hardware acceleration

- NVIDIA Ollama inference is validated on the documented Supported-host
  boundaries with an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0.
  Other driver, toolkit, and GPU combinations remain unvalidated.
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
- Application backup format version 1 protects catalog-declared managed files, detected SQLite databases, and generated secrets for exact existing-installation restore. Restore records swaps and recovery in a durable helper journal.
- SQLite databases that depend on application-provided collations, tokenizers,
  functions, or virtual-table modules cannot receive KITPro's full generic
  `integrity_check`. KITPro records `structural-pages-passed` only after an
  extension-independent walk accounts for every database and freelist page
  and the foreign-key check succeeds. Other SQLite verification errors fail
  closed.
- Application backups normalize safe relative symlinks to ordinary files only
  when they resolve to regular files inside the same managed storage root.
  Symlink identity is not preserved. Other symlinks and special files fail the
  backup.
- Imported external data is reference-only. KITPro records the trusted binding but does not copy NAS, media, music, audiobook, or SFTPGo external file content.
- Backup destinations are local and archives are not encrypted by KITPro. Scheduling, retention, object storage, bare-host recovery, and host-to-host restore are not implemented.
- Generated application secrets are stored as root-bound plaintext in the
  helper database and in restrictive local application backups. KITPro does
  not claim encryption at rest. Directory and file permissions, helper
  confinement, and the credential-reveal boundary prevent normal unprivileged
  access, but host root remains trusted.
- PostgreSQL and MariaDB backup strategies are not implemented. No current visible catalog application deploys either database engine.
- Rollback of irreversible upstream schema migrations is not promised.
- Mixed restore state or ambiguous ownership can require a reviewed recovery decision. KITPro does not guess which tree or runtime owns the installation.
- A running container is runtime evidence, not application readiness. The current catalog does not provide manifest-defined readiness probes.
- Same-port replacement requires a bounded cutover interruption because the old and replacement runtimes cannot own the same host binding concurrently.
- Failed restore trees can be retained under an operation-specific name for investigation. Automated long-term retention and deletion policy is not implemented.

## Application and network boundaries

- Open WebUI and Ollama are separate installations. Cross-installation trusted service discovery is not implemented, and KITPro does not inject an unvalidated backend URL.
- Immich is not supported. Its current production topology requires shared component secrets, health-gated dependencies, bounded database shared memory, atomic multi-component updates, and database-aware backup/rollback that are not current trusted primitives.
- Forgejo exposes HTTP only. KITPro does not publish Git-over-SSH.
- Plex is a constrained CPU-only profile. KITPro does not expose discovery,
  DLNA, GPU devices, router configuration, or automatic remote access.
- Nextcloud is Experimental and uses a small-instance SQLite profile. It has
  no external database, Redis, high availability, public HTTPS, or general
  sync-client workload claim.
- Pi-hole is an Experimental Network Service. It publishes DNS on TCP and UDP
  port 53 and a dynamic loopback administration endpoint. KITPro does not
  configure DHCP, NTP, HTTPS, host networking, router settings, or client DNS.
- Syncthing is Experimental and supports manual peers over explicit TCP
  addresses only. Global discovery, local discovery, relays, NAT traversal,
  QUIC, and UDP publication are disabled. KITPro does not configure peers or
  copy the external `/sync` tree into backups.
- The archived original File Browser is not supported. SFTPGo is the maintained, first-run-authenticated read-write catalog choice.
- LocalAI is excluded because its official image can fetch an unsigned mutable backend during model installation; pinning only the outer image does not provide end-to-end provenance.
- Clustering, high availability, and automatic failover are not implemented.
- Not every reconciliation, repair, backup, restore, or recovery workflow has a complete browser interface.
- Destructive application-data deletion is not a completed lifecycle.

## Update boundary

- Application updates are trusted-catalog and administrator initiated.
- Native package managers remain authoritative for KITPro package updates.
- Pre-update backups protect bounded control-plane state, not imported external data or the complete host.
- Cleanup after a safely committed generation can fail independently. KITPro records `cleanup_pending`; the committed generation remains successful and requires an explicit cleanup repair.
