# Debian package acceptance — 2026-09-13

Result: **DEB PACKAGE GATE: PASS**

## Cross-platform package certification addendum

Ubuntu 26.04 certification produced the final supported cross-platform package,
`kitpro-server_0.1.0~alpha2_amd64.deb`. The same alpha2 artifact was installed
and regression-tested on Debian 13.7 without a distro-specific package branch.
Its measured size is `7,945,436` bytes and its SHA-256 is
`81def31335bfa3c00b3ab789f5ab13557b0d8a83328d33b7d5c12a6ae694c55c`.
The alpha1 measurements below remain the immutable record of the original
Debian package gate; alpha2 supersedes alpha1 as the retained supported build.

This record is additive. It does not replace earlier production-slice,
FreshRSS, or controlled-exposure results.

## Artifact and build

| Check | Result | Evidence |
| --- | --- | --- |
| Package | PASS | `kitpro-server_0.1.0~alpha1_amd64.deb`, 7,946,552 bytes |
| Package SHA-256 | PASS | `2c841d6e5266ef82b0da49b74eb21a373718da276529c17584484e5fe55092ab` |
| SBOM | PASS | CycloneDX 1.5 JSON, 12 components, with adjacent SHA-256 |
| Source identity | PASS | `710a5b45eb42446cd42f43054984484fbbc1c4b4`; build metadata records that the reviewed source tree contained the packaging changes |
| Toolchain | PASS | `go1.27.0-X:nodwarf5`, `CGO_ENABLED=0`, `linux/amd64` |
| Build policy | PASS | `-trimpath`, `-buildvcs=false`, stripped binaries, source-commit `SOURCE_DATE_EPOCH` |
| Reproducibility | PASS | Two independent alpha1 package builds produced identical SHA-256 values |
| Binary metadata | PASS | Both binaries report version `0.1.0~alpha1`, source commit, and deterministic build timestamp |
| Binary form | PASS | API 13,115,552 bytes; helper 10,813,600 bytes; both stripped and statically linked |

The SBOM generator is pinned to `cyclonedx-gomod@v1.7.0`. It reported that no
project license could be detected. The Debian copyright file therefore records
this as an unpublished proprietary validation artifact rather than implying
redistribution rights.

## Package inspection

| Check | Result | Evidence |
| --- | --- | --- |
| Control metadata | PASS | `dpkg-deb --info` reports package `kitpro-server`, version `0.1.0~alpha1`, amd64, and only `adduser`, `apparmor`, and `systemd` dependencies |
| Payload | PASS | `dpkg-deb --contents` contains the two binaries, three systemd units, tmpfiles policy, AppArmor policy, defaults, manpages, and documentation; no runtime database is shipped |
| Maintainer scripts | PASS | `dash -n` and ShellCheck passed for `preinst`, `postinst`, `prerm`, and `postrm` |
| systemd static validation | PASS | `systemd-analyze verify` accepted the API service, helper service, and helper socket |
| AppArmor syntax | PASS | Debian's `apparmor_parser -Q -T` accepted the packaged profile |
| Lintian | PASS | Lintian 2.122.0 produced no findings for the final artifact |
| Reproducible static test | PASS | Package static test checked contents, policy invariants, checksums, and identical repeated builds |

Docker Engine remains an explicit external prerequisite instead of a package
dependency. This avoids choosing between Debian's `docker.io` and Docker Inc.'s
`docker-ce` package and is compatible with the planned Ubuntu evaluation. The
pre-install check was measured with Docker healthy, with the socket hidden, and
with the daemon stopped. The latter two failed before unpack with explicit
messages and did not install or reconfigure Docker.

## Debian 13 installation

Validation host: Debian GNU/Linux 13 (trixie), amd64, Docker Engine 29.8.0.

| Check | Result | Evidence |
| --- | --- | --- |
| Fresh install | PASS | An existing manual deployment was saved under `/var/backups/kitpro-prepackage-20260913T1915Z`; the alpha0 package then installed from production-shaped empty default databases without manual permission repair |
| Identity | PASS | `kitpro-api` was created as UID/GID 987 with only its own group; it is not in `docker`; the helper runs as root |
| Paths | PASS | API state is `0750 kitpro-api:kitpro-api`; helper state is `0700 root:root`; `/srv/kitpro` is `0750 root:root`; `/run/kitpro` is `0750 root:kitpro-api`; the helper socket is `0660 root:kitpro-api` |
| Database initialization | PASS | Both production migration paths created schema version 3 databases with WAL, foreign keys, and the configured busy timeout |
| AppArmor | PASS | `/usr/libexec/kitpro-helper` is loaded in enforce mode and the real helper ran under that profile |
| systemd | PASS | API and helper socket are enabled and active; helper socket activation works |
| Hardening | PASS | API/helper exposure scores were 2.9 and 3.0 (`OK`); empty capability sets, `NoNewPrivileges`, private devices/tmp, strict filesystem protection, kernel protections, address-family restrictions, lock-personality, MDWE, syscall filter, and restrictive umask remain active; `RestrictSUIDSGID` remains absent |
| First-run authentication | PASS | Setup and login completed through the production HTTP flow with a disposable random administrator; no credential was written to the repository or result record |
| Dashboard/helper | PASS | The authenticated dashboard and API/helper socket boundary worked; the API account remained unable to traverse the root-owned helper database directory or access the root:docker socket |

An initial package run exposed two packaging defects. The helper needed read
access to `/etc/passwd` to resolve the packaged API account, and AppArmor used a
stale reproducible-build cache unless invoked with `-T`. Both were fixed
narrowly and retested. Remove/reinstall then exposed that dpkg remembers a
deleted conffile. The final package keeps the live profile as a Debian conffile
and also ships an immutable recovery copy under `/usr/share/kitpro-server`; a
reinstall restores the live profile before fail-closed parsing and loading.

## Application and exposure smoke test

| Check | Result | Evidence |
| --- | --- | --- |
| FreshRSS install | PASS | Authenticated catalog install created installation `inst-641791e27460e346` and one generation-2 runtime after exposure recreation |
| Helper authority | PASS | The helper database contains the trusted ownership row and five durable operation receipts |
| Internal baseline | PASS | FreshRSS initially had no host publication and returned HTTP from a disposable same-network diagnostic container |
| Controlled exposure | PASS | Loopback assignment persisted as `127.0.0.1:20000` to declared container TCP port 80; no wildcard binding exists |
| HTTP | PASS | `curl http://127.0.0.1:20000/` returned FreshRSS HTTP 302 after install, upgrades, removal cycles, and reboot |
| Persistent data | PASS | `/srv/kitpro/apps/freshrss/inst-641791e27460e346/data/KITPRO-PACKAGE-PERSISTENCE-001` retained SHA-256 `9b97551a83f9fa2426453859a654d7d4ddc5e40a340b447fe8386601bc0255b9` |
| Duplication | PASS | Exactly one deterministic FreshRSS runtime remained throughout upgrade, reinstall, purge/reinstall, and reboot |

The final validation runtime remains loopback-only. The package never changes
the dashboard's loopback default and never enables public or LAN exposure.

## Upgrade, failure, removal, and reboot

| Check | Result | Evidence |
| --- | --- | --- |
| A to B upgrade | PASS | `0.1.0~alpha0` upgraded to `0.1.0~alpha1`; both writers stopped, each trust domain created a validated `VACUUM INTO` backup, migrations ran, and services recovered |
| Backup restore sanity | PASS | Control and helper backups opened at separate disposable paths; `integrity_check` returned `ok`, foreign-key checks were empty, and expected operations/installations/receipts/ownership were present |
| Upgrade failure | PASS | A deliberately invalid disposable control DB caused pre-upgrade to fail with `file is not a database (26)`; unpack did not proceed and the old API/socket restarted |
| Downgrade | PASS | Alpha1 to alpha0 was rejected by `preinst` before unpack; alpha1 and its services remained intact |
| Remove | PASS | Package binaries, units, tmpfiles policy, and live AppArmor profile were removed; config, both databases, backups, account, Docker runtime, and application data remained |
| Reinstall | PASS | The same alpha1 artifact restored the profile from its immutable package copy, reloaded it enforcing, migrated preserved state, and recovered the existing FreshRSS endpoint without duplication |
| Purge | PASS | Package config and package assets were removed; trusted state, backups, logs, state-owning account, Docker runtime, and `/srv/kitpro/apps` remained by deliberate conservative policy |
| Purge/reinstall | PASS | Defaults, AppArmor policy, systemd units, and services were restored; the same trusted application state and endpoint recovered |
| Reboot | PASS | After a full VM reboot and no repair, Docker, API, and helper socket were active; tmpfiles recreated `/run/kitpro` and its `0660` socket; AppArmor remained enforcing; DB integrity, exposure assignment, one runtime, endpoint, and marker all remained intact |

Package downgrade is intentionally unsupported. Purge is not an application
data deletion or trusted-ownership reset operation: silently deleting the
helper store while Docker resources survive would remove safe lifecycle
authority. A future explicit destructive product operation must own that
transition.

## Repository validation

- `go test ./...`: PASS
- `go vet ./...`: PASS
- gofmt check: PASS
- reconciliation prototype: PASS, 20/20
- privilege-boundary fixture: PASS, 35/35 (the local restricted sandbox denied
  Unix-socket creation, so the identical fixture was rerun outside that sandbox)
- Docker installer detection suite: PASS, 24/24
- Bash syntax: PASS
- ShellCheck: PASS
- Markdown local-link validation: PASS
- `git diff --check`: PASS
- sensitive-pattern and generated-state scan: PASS
- direct Go dependency change: none; pinned `govulncheck` was therefore not
  required by this milestone's policy

## Platform statement

Debian 13 amd64 is the only supported package target established by this
record. Ubuntu 26.04 LTS amd64 is a compatibility design target, and Rocky Linux
10 remains experimental. This record makes no Ubuntu support claim.
