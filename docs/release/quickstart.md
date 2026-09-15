# KITPro Server public alpha quickstart

## Debian or Ubuntu

1. Use a supported amd64 host with rootful Docker and enforcing AppArmor.
2. Install Docker with `tools/install-docker.sh` when it is not already present.
3. Verify the downloaded package checksum, then install the `.deb` with `apt install ./kitpro-server_0.1.0~alpha2_amd64.deb`.
4. Open the local dashboard at `http://127.0.0.1:8080/` and create the first administrator.
5. Install an application from the catalog. Applications start internal-only.
6. Choose loopback or the configured LAN address to expose a declared service.

Persistent data is under `/srv/kitpro/apps/<application>/<installation>/`.
Removing a runtime preserves the installation and data; package upgrades back
up KITPro control state before migrations. Application data backup is separate.

## Arch Linux

Use a fully updated system with `linux-lts`, then install the native
`kitpro-server-0.1.0_alpha2-1-x86_64.pkg.tar.zst` using `pacman -U`.
Always use complete `pacman -Syu` upgrades; partial upgrades are unsupported.

For recovery, inspect the operation history and validated backups before
changing state. Do not grant the API account Docker socket access.
