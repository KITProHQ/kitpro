# KITPro alpha.13 release checklist

- [x] Debian 13, Ubuntu 26.04 LTS, and Arch Linux Supported-host qualification is current.
- [x] Each Supported host uses a locally selected non-overlapping Docker address pool and passes the positive and negative preflight checks.
- [x] Rocky Linux 10 is labeled Experimental and its alpha.13 Podman lifecycle limitation is recorded.
- [x] Exactly 20 applications are visible. The five alpha.13 additions keep their documented constraints.
- [x] Lifecycle, runtime removal, networking, credentials, backup/restore, reboot, and security checks are current on Supported hosts.
- [x] The Debian alpha.12 to alpha.13 and schema 13 to 14 migration is qualified.
- [x] Current documentation records Supported and Experimental boundaries, runtime removal, credentials, plaintext secret storage, and networking limits.
- [ ] Freeze the final documentation commit and record its HEAD and tree.
- [ ] Run the complete repository suite on the final frozen source.
- [ ] Build and inspect the final Debian, Arch, Rocky, SELinux, and SBOM artifacts.
- [ ] Verify artifact reproducibility, embedded source identity, and SHA-256 checksums.
- [ ] Install the final artifacts on the qualification hosts and rerun affected source-identity and recovery checks.
- [ ] Complete the final release manifest and checksum manifest outside the frozen source tree.
- [ ] Confirm a clean branch with no stale version strings or secret material.
- [ ] Obtain human authorization before push, tag, GitHub release creation, or artifact publication.
