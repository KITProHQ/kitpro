# Debian package assets

## Rocky Linux 10 RPMs

Build the experimental Rocky packages on Rocky Linux 10 with:

```sh
./packaging/build-rpm.sh 0.1.0~alpha11
```

The build emits `kitpro-server`, `kitpro-selinux`, and source RPMs. The main
package depends on Rocky's Podman 5, crun, container-selinux, firewalld, and
systemd stack. It installs a Rocky-specific helper unit and does not configure
Docker. The SELinux subpackage owns only the persistent file context for
`/srv/kitpro/apps`; it contains no allow rule, permissive domain, or boolean.

Normal RPM removal archives generated Quadlets and runtime secrets while
preserving databases and application data. An acknowledged purge is available
through `kitpro-server-uninstall`. See
[`docs/install-rocky-linux.md`](../../../docs/install-rocky-linux.md).

Native Arch Linux packaging lives under `packaging/arch/`. Build it as an
unprivileged user with:

```sh
./packaging/build-arch-package.sh 0.1.0_alpha11
```

The builder creates a deterministic source archive, substitutes its checksum
into the build-only PKGBUILD, invokes `makepkg`, and writes the package,
SHA-256, and build metadata under `dist/`. Outside a Git checkout,
`KITPRO_SOURCE_COMMIT` and `SOURCE_DATE_EPOCH` are mandatory so source-tar
builds remain attributable and reproducible. See
[`docs/install-arch-package.md`](../../../docs/install-arch-package.md).

## Debian-compatible package

Build the Debian 13 and Ubuntu Server 26.04 LTS amd64 package with:

```sh
./packaging/build-package.sh 0.1.0~alpha11
```

The output is `dist/kitpro-server_0.1.0~alpha11_amd64.deb` plus SHA-256 and
build-metadata files. `SOURCE_DATE_EPOCH` may override the default source-commit
timestamp. The build is static (`CGO_ENABLED=0`) and uses `-trimpath`.
Generate the CycloneDX SBOM separately with
`./packaging/generate-sbom.sh 0.1.0~alpha11`; the generator version is pinned in
that script and the SBOM receives its own adjacent checksum.

Docker Engine is a pre-existing runtime prerequisite rather than a Debian
package dependency. This avoids binding KITPro to either Debian's `docker.io`
package or Docker Inc.'s `docker-ce` package across Debian and Ubuntu. Package
pre-installation fails clearly unless Docker is active and its Unix socket is
present.

The package creates the unprivileged `kitpro-api` account. The helper runs as
root under the enforcing AppArmor profile and systemd sandbox. The API account
is never added to the Docker group. The helper unit intentionally omits
`RestrictSUIDSGID` because the measured Debian/Rocky baseline showed it breaks
`openat2`.

The live AppArmor profile is a Debian conffile. Removal unloads and deletes it;
the package also carries an immutable copy under `/usr/share/kitpro-server` so
reinstall can restore a profile that dpkg remembers as administrator-deleted
before performing the fail-closed syntax check and load.

Normal remove preserves configuration, databases, backups, logs, and all
application data. Purge removes the package conffile but deliberately retains
control/helper state, the service identity that owns it, logs, backups, and
`/srv/kitpro`. Losing helper ownership state while Docker resources remain
would violate the trusted-ownership model. A future explicit data-removal tool
must own that destructive transition. Downgrades are unsupported and rejected
by `preinst` before package unpack.
