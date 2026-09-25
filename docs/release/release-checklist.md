# KITPro alpha release checklist

- [ ] Source tree reviewed and sensitive-data scan is clean.
- [ ] Go tests, vet, formatting, shell, AppArmor, and systemd checks pass.
- [ ] Control and helper migrations are ordered, backed up, and validated.
- [ ] The Debian/Ubuntu `.deb` and Arch package build paths produce checksums, SBOMs, and build metadata.
- [ ] Debian 13, Ubuntu 26.04 LTS, and Arch Linux platform acceptance evidence is current.
- [ ] Every Supported Docker host records a locally selected non-overlapping address pool, passes the positive preflight, and preserves negative failure-message evidence.
- [ ] Rocky Linux 10 and Podman are labeled Experimental.
- [ ] FreshRSS and Paperless lifecycle, exposure, persistence, and reconciliation evidence is current.
- [ ] Native package upgrade, remove, reinstall, and failure handling are documented.
- [ ] Trusted application release update and backup/restore evidence is current.
- [ ] Release manifest and support boundary are reviewed.
- [ ] Git history is ready for a release tag (tagging and publication require explicit approval).
