# Debian package assets

Native Arch Linux packaging lives under `packaging/arch/`. Build it as an
unprivileged user with:

```sh
./packaging/build-arch-package.sh
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
./packaging/build-package.sh
```

The output uses the native Debian spelling of the public version in the root
`VERSION` file (for example, `0.1.0-alpha.2` becomes
`dist/kitpro-server_0.1.0~alpha2_amd64.deb`) and includes SHA-256 and
build-metadata files. `SOURCE_DATE_EPOCH` may override the default source-commit
timestamp. The build is static (`CGO_ENABLED=0`) and uses `-trimpath`.
Generate the CycloneDX SBOM separately with
`./packaging/generate-sbom.sh`; the generator version is pinned in
that script and the SBOM receives its own adjacent checksum.

After both packages and the SBOM are built from a clean commit, stage the
public release assets with:

```sh
./packaging/stage-release.sh ./dist /path/to/release-directory
```

The staging command refuses mismatched versions, source commits, dirty-build
metadata, or SBOM identity. It emits one combined build-metadata file, a
machine-readable release manifest, and a checksum file without writing those
generated release records back into source history.

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
