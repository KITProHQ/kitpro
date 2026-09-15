# Known alpha limitations

- NVIDIA acceleration is validated on the supported Debian, Ubuntu, and Arch boundaries with an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0. Other driver, toolkit, and GPU combinations remain unvalidated.
- NVIDIA initially permits exactly one unambiguous GPU. Stable multi-GPU selection is future work.
- Arbitrary USB, input, TTY, KVM, disk, memory, and other host devices remain unsupported. There is no raw device escape hatch.
- Ollama's trusted image supports CPU and optional NVIDIA. AMD ROCm and Intel image variants are future catalog work.
- AMD device scoping has live local evidence, but AMD compute and Intel accelerated workloads remain unvalidated.
- Jellyfin supports one administrator-approved read-only media root. Multiple libraries and write-enabled media changes are not supported. NVIDIA transcoding is not certified until the application-level live test completes.
- Immich is not admitted: shared component secrets, health-gated dependencies, bounded database shared memory, and database-aware rollback are not yet trusted primitives.
- Syncthing is not admitted. UDP publication alone does not fix the upstream-documented LAN discovery limitation under Docker bridge networking, and KITPro does not allow host networking.
- The archived original File Browser is not admitted. SFTPGo is the maintained, first-run-authenticated read-write catalog choice.
- Read-write trusted roots are exclusive. KITPro does not merge concurrent writers or provide file-level locking.
- KITPro registers existing local or host-mounted network storage; it does not mount NFS/SMB shares or manage their credentials.
- Imported data is not included in KITPro control-plane backups. Root reassociation after restore is explicit.
- LocalAI is excluded because its official image fetches an unsigned mutable backend during model installation; pinning only the outer image is not sufficient provenance.
- Cross-installation Open WebUI-to-Ollama discovery is not implemented. Isolated application networks remain the security boundary.
- KITPro grants bounded device access but does not schedule GPU work or reserve VRAM when multiple applications share one GPU.

- Only the platforms in the support matrix are certified.
- Rootful Docker and enforcing AppArmor are required.
- The catalog is intentionally limited; arbitrary Compose and user manifests
  are not accepted.
- Application-data backup, HA, clustering, and automatic disaster recovery
  are not implemented.
- Application updates are trusted-catalog and administrator initiated; native
  package managers remain authoritative for KITPro updates.
- Rollback of irreversible schema migrations is not promised.
- LAN exposure binds one configured host address; wildcard/public-Internet
  exposure, reverse proxy, domains, and TLS automation are out of scope.
