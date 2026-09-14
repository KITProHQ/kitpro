# Ubuntu 26.04 package certification

Date: 2026-09-13 America/Los_Angeles (2026-09-14 UTC on the validation host)

Result: **PASS**

## Artifact and compatibility fix

The original Debian artifact `kitpro-server_0.1.0~alpha1_amd64.deb`
(`2c841d6e5266ef82b0da49b74eb21a373718da276529c17584484e5fe55092ab`)
installed on Ubuntu but exposed a real incompatibility: Ubuntu's AppArmor 5
kernel mediation denied `SO_TYPE` inspection on the systemd-created helper
socket after the service entered its private mount namespace. Strace recorded
`getsockopt(..., SOL_SOCKET, SO_TYPE, ...) = -1 EACCES`.

The generic package fix adds `flags=(attach_disconnected)` to the existing
helper profile. It does not add network access or broaden filesystem access.
Integration then exposed a second generic defect: runtime removal deleted the
helper's installation trust record and made safe recreation impossible. The
helper now clears only disposable container/network identity while retaining
the installation, storage, release, generation, and exposure trust anchors.

The replacement cross-compatible artifact is:

| Field | Measured value |
| --- | --- |
| Package | `kitpro-server` |
| Version | `0.1.0~alpha2` |
| Architecture | `amd64` |
| Source commit | `9072205239974dbd99958713726cb05f8dc96b04` plus the recorded uncommitted compatibility fix |
| Go toolchain | `go1.27.0-X:nodwarf5` |
| Build flags | `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, `-ldflags=-s -w` |
| Size | `7,945,436` bytes |
| SHA-256 | `81def31335bfa3c00b3ab789f5ab13557b0d8a83328d33b7d5c12a6ae694c55c` |
| SBOM SHA-256 | `c0d905905a603c58689581a6f908e7984758b7d4e1f5e0c41d9f0b391425fe6a` |
| Reproducibility | PASS; two clean builds produced the same package hash |

## Host baseline

| Requirement | Result | Evidence |
| --- | --- | --- |
| Host | PASS | Proxmox VM 502, Ubuntu Server 26.04.1 LTS (`resolute`), amd64 |
| Kernel/systemd | PASS | Linux `7.0.0-31-generic`; systemd 259 |
| Control groups | PASS | unified cgroup v2 |
| Storage/capacity | PASS | ext4 root; 39,838,908,416 bytes total and 30,136,401,920 bytes free at baseline |
| AppArmor | PASS | enabled and enforcing, parser 5.0.2 |
| Firewall baseline | PASS with caveat | UFW inactive; nftables initially empty; no firewall policy was weakened |
| Network | PASS | `10.10.0.116/24` on `ens18`, default route via `10.10.0.1` |

## Docker and package lifecycle

| Requirement | Result | Evidence |
| --- | --- | --- |
| Docker installation | PASS | Repository `tools/install-docker.sh --yes` selected Docker's Ubuntu `resolute` repository without `--grant-user-access` |
| Docker runtime | PASS | Engine/CLI 29.8.0, containerd 2.3.5, Buildx 0.37.1, Compose 5.5.1, overlayfs, cgroup v2 |
| User boundary | PASS | `josh` and `kitpro-api` were not added to `docker`; direct API-identity socket access failed |
| Package inspection/install | PASS | Exact transferred hash matched; APT installed normal dependencies and package assets without manual unpacking |
| Filesystem layout | PASS | Binaries, units, profile, tmpfiles, state roots, and `/srv/kitpro` had production ownership/modes |
| systemd | PASS | API and helper socket active; socket activation, empty capability bounds, `NoNewPrivileges`, `MemoryDenyWriteExecute`, and `AF_UNIX`-only helper policy retained |
| AppArmor | PASS | Profile parsed, loaded enforcing, and attached to the live helper; helper Docker operations succeeded; child executable attempts were denied |
| Upgrade/reinstall | PASS | Pre-upgrade `VACUUM INTO` backups, migrations, profile reload, and services succeeded for alpha1 to alpha2 and alpha2 reinstall |
| Remove/reinstall | PASS | Package files/services were removed, Docker/runtime/state/data remained, and reinstall restored services and confinement |
| Purge safety | PASS | Configuration was purged while control/helper state, backups, service identity, Docker runtime, and `/srv/kitpro/apps` remained |
| Downgrade | PASS (policy) | Package pre-install policy rejects lower Debian versions before unpack; downgrade was not used as a recovery mechanism |

## Product acceptance

| Requirement | Result | Evidence |
| --- | --- | --- |
| First-run authentication | PASS | Disposable administrator created through production setup; setup closed; login and authenticated dashboard succeeded |
| Auth boundary | PASS | Anonymous mutation 401, missing CSRF 403, bad Origin 403, valid authenticated request 202; rejected exposure requests did not advance runtime generation |
| FreshRSS install | PASS | Catalog install used immutable digest `sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21`; two installation-scoped stores; no host publication by default |
| Internal HTTP | PASS | Same-network diagnostic returned HTTP 302 |
| Loopback exposure | PASS | Exact `127.0.0.1:20000 -> 80/tcp`; loopback HTTP 302; `10.10.0.116:20000` was unreachable |
| LAN exposure | PASS | Exact `10.10.0.116:20000 -> 80/tcp`; VM and an authorized LAN peer both received HTTP 302; no wildcard IPv4/IPv6 binding |
| Stable installation/data | PASS | Runtime removal retained helper trust; recreation kept installation/storage and marker while generation advanced |
| Persistent marker | PASS | `KITPRO-UBUNTU-PERSISTENCE-001` remained `035513658f9ba8601b92b789ee5263e52de5e29856cf6e46d8d81a6f48fcd36d` through lifecycle and exposure transitions |
| Port stability | PASS | Assigned port remained stable across recreation, API/helper restart, Docker restart, package reinstall, and reboot |
| Collision handling | PASS | A foreign listener on candidate port 20002 was untouched and a new installation received 20003; a foreign listener on persisted port 20001 caused explicit `host port unavailable` failure without reassignment |
| Disable/re-enable | PASS | Disable recreated an internal-only runtime, retained port 20001 and storage, and preserved internal HTTP; re-enable restored the same port |
| Reconciliation | PASS | Exact, externally missing container, running/stopped foreign network member security drift, and cleared drift classifications matched policy |
| Docker restart | PASS | Existing generation returned with exact binding, same port, HTTP 302, and exact reconciliation |
| VM reboot | PASS | Docker/API/socket/AppArmor returned without repair; one runtime, same installation/generation/address/port, marker, receipt history, HTTP 302, exact reconciliation |
| Backup sanity | PASS | Production control/helper backups opened independently; integrity `ok`, foreign-key checks clean, and expected installation/exposure/receipt/ownership rows present |
| UI | PASS | Dashboard rendered `Web Interface`, `LAN`, and the safe exact endpoint without exposing storage paths or Docker structures |
| Audit | PASS | Exposure allocation/reuse/application/failure/collision and reconciliation drift events carried operation/installation/service fields; no password, session, or CSRF material appeared |

## Ubuntu firewall observation

UFW was inactive throughout and was not changed. Docker installed exact-address
nftables DNAT for `10.10.0.116:20000` to the application bridge; no wildcard or
IPv6 publication appeared. Docker-published ports can bypass UFW's ordinary
filter path, so UFW alone is not a supported exposure boundary. Custom UFW or
nftables deployments require separate validation; the certified Phase 1
boundary is KITPro's exact bind address and port.

## Debian regression and conclusion

The same alpha2 artifact upgraded Debian 13.7 VM 500 from alpha1. Upgrade
backups and migrations succeeded, Docker/API/socket stayed active, both DB
integrity checks passed, the helper profile parsed and attached enforcing, and
the existing FreshRSS loopback endpoint returned HTTP 302. No Debian-specific
branch or separate Ubuntu package was introduced.

**Compatibility decision:** Debian 13 amd64 remains primary/reference. Ubuntu
Server 26.04 LTS amd64 is supported by the same `kitpro-server` package. Rocky
Linux 10 amd64 remains experimental.
