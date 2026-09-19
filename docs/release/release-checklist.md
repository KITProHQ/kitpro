# KITPro alpha release checklist

- [ ] Source tree reviewed and sensitive-data scan is clean.
- [ ] Go tests, vet, formatting, shell, AppArmor, and systemd checks pass.
- [ ] Control and helper migrations are ordered, backed up, and validated.
- [ ] Debian and Arch packages build reproducibly with checksums, SBOMs, and build metadata.
- [ ] Debian and Arch platform acceptance evidence is current.
- [ ] Ubuntu evidence, if mentioned, is labeled development-only; Rocky Linux and Podman are labeled Experimental.
- [ ] FreshRSS and Paperless lifecycle, exposure, persistence, and reconciliation evidence is current.
- [ ] Native package upgrade, remove, reinstall, and failure handling are documented.
- [ ] Trusted application release update and backup/restore evidence is current.
- [ ] Release manifest and support boundary are reviewed.
- [ ] Git history is ready for a release tag (tagging and publication require explicit approval).
