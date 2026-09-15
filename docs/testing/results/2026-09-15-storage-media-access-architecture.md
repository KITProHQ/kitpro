# Storage and media access architecture acceptance — 2026-09-15

## Gate

`STORAGE + MEDIA ACCESS ARCHITECTURE: PASS`

The implementation admits only administrator-registered roots and trusted
manifest slots. It has no API, manifest, or helper request field for a raw
Docker bind. Debian completed the full acceptance matrix; Ubuntu and Arch
completed package-upgrade, registration, Jellyfin, read-only, private-exposure,
and reboot smoke tests.

## Architecture and threat model

KITPro-managed storage remains under
`/srv/kitpro/apps/<application>/<installation>/<slot>/` and stays owned by
KITPro. Imported storage remains owned by the administrator. The helper stores
the root ID, display name, canonical host path, permitted mode, device
major/minor, root inode, filesystem type, mount source, mount point, and
network-backed classification.

Manifest schema 4 declares an `external_storage` slot with ID, fixed container
target, `read-only` or `read-write` mode, required/optional semantics, and a
plain-language purpose. It cannot declare a host path. The control-plane
protocol carries only slot and root IDs. The helper reloads the embedded trusted
manifest, validates the root and access ceiling, and constructs the bind.

The forbidden-root policy rejects `/`, `/boot`, `/dev`, `/etc`, `/home`,
`/proc`, `/root`, `/run`, `/sys`, `/usr`, Docker/containerd/containers data
roots, KITPro state/data, and runtime sockets. Relative paths, traversal,
missing directories, and any path whose canonical form differs because of a
symbolic link fail closed. A positive policy permits only dedicated children
of `/mnt`, `/media`, `/data`, or `/srv`, never those top-level parents.

Threats reviewed: malicious manifests, compromised API requests, protected
host roots, path traversal, symlink escape, stale root identity, missing NAS,
read-only downgrade, unexpected binds, in-use root deletion, component-wide
mount spread, and restore onto a different host. Exact bind-set reconciliation
and helper-owned identity state mitigate these paths. Root-equivalent Docker,
the kernel, and an already-root host attacker remain outside the isolation
guarantee.

## Upstream research and decisions

| Candidate | Authoritative deployment facts | Decision |
| --- | --- | --- |
| Jellyfin | Official `jellyfin/jellyfin`; config `/config`; cache `/cache`; media bind; HTTP 8096; bridge networking supported; host networking is optional for DLNA | ACCEPT |
| Syncthing 2.1.5 | Official `syncthing/syncthing`; `/var/syncthing`; GUI 8384/TCP; sync 22000/TCP+UDP; discovery 21027/UDP; upstream strongly recommends host networking for LAN discovery | REJECT FOR CURRENT MODEL: KITPro has no host networking or typed UDP multi-service exposure |
| Immich | Official production path is Docker Compose with server, PostgreSQL, Redis, and machine learning; photo library is read-write; PostgreSQL must use suitable local storage | REJECT FOR CURRENT MODEL: needs a larger multi-container readiness and read-write data-lifecycle acceptance |

Jellyfin trusted release: `12.1`, GPL-2.0-or-later,
`docker.io/jellyfin/jellyfin@sha256:326be1010b16c92e492f6c7dd6fd105943db84ce723c73183279a1ab357b8f9b`
for `linux/amd64`. Syncthing's researched immutable `linux/amd64` image was
`docker.io/syncthing/syncthing@sha256:84dcf202b0890f795c4c3899d35a5ac7369bb8b72b5c270078c50247da4ddeef`;
it was not admitted.

Authoritative sources: [Jellyfin container deployment](https://jellyfin.org/docs/general/installation/container/), [Syncthing container guide](https://github.com/syncthing/syncthing/blob/main/README-Docker.md), [Syncthing Dockerfile](https://github.com/syncthing/syncthing/blob/main/Dockerfile), [Immich Docker Compose installation](https://docs.immich.app/install/docker-compose/), and [Immich requirements](https://docs.immich.app/install/requirements/).

## Path and access validation

| Test | Result | Evidence |
| --- | --- | --- |
| Protected roots and runtime sockets | PASS | Unit tests reject the bounded deny list, Docker/containerd/containers roots, KITPro state/data, and socket names |
| Traversal and relative input | PASS | `..` segments and non-absolute input rejected before inspection |
| Symlink root/escape | PASS | `EvalSymlinks` result must equal the cleaned submitted path |
| Read-only enforcement | PASS | Live Jellyfin `touch /media/should-not-write` failed with `Read-only file system`; Docker bind ended in `:ro` |
| Read-write ceiling | PASS | Helper rejects a read-write slot against a read-only root; a read-write test root resolves only its exact canonical directory and fixed manifest target |
| Unexpected bind | PASS | Reconciliation test injecting `/etc:/host:ro` returns storage security drift |
| In-use root deletion | PASS | Helper refuses removal while a binding exists; schema foreign key is `ON DELETE RESTRICT` |

No read-write application was admitted in this milestone, so no production
catalog entry receives write access to imported data. The generic read-write
path is typed and tested but remains unused until an application passes its own
data-lifecycle acceptance.

## Jellyfin live acceptance

A four-second synthetic MP4 was used on all hosts. Its SHA-256 remained
`4bc283ad1ea2119b1405eb3ed75354f358f0faa66b50386510eabb6a0d5a8741`.
No private media was used.

| Check | Debian 13 VM 500 | Ubuntu 26.04 VM 502 | Arch `linux-lts` VM 503 |
| --- | --- | --- | --- |
| Package upgrade/schema 7 | PASS | PASS | PASS |
| Register `/mnt/kitpro-test-media` through authenticated API | PASS | PASS | PASS |
| Exact immutable image | PASS | PASS | PASS |
| Managed config/cache | PASS | PASS | PASS |
| Imported `/media:ro` | PASS | PASS | PASS |
| No host publication by default | PASS (`PortBindings={}`) | PASS | PASS |
| Healthy runtime | PASS | PASS | PASS |
| Read-only write attempt | PASS | PASS | PASS |
| Recreation/persistent config | PASS | Smoke PASS | Smoke PASS |
| Reboot recovery | PASS | PASS | PASS |
| Exact reconciliation | PASS | Smoke PASS | Smoke PASS |

Ubuntu and Arch test disks were expanded from 40 GB to 60 GB because the
official Jellyfin image plus Jellyfin's 2 GiB startup reserve exhausted the
original system volume. The filesystems were extended safely; the disks were
not shrunk afterward.

Debian missing-storage drill: renaming the registered root produced
`security_drift` with `trusted storage unavailable or changed`; recreation
failed before replacing the running generation. Restoring the exact root
allowed recreation and exact reconciliation. The imported file was unchanged.

## NAS and restore behavior

The helper classifies NFS, CIFS/SMB, SSHFS, 9p, Ceph, and Gluster filesystem
types from `/proc/self/mountinfo`. KITPro deliberately does not mount network
shares or store NAS credentials. No disposable NFS/CIFS test share was
available, so live network-disconnect testing was not claimed. The Debian
missing-root drill proves the required fail-closed path without risking a real
NAS.

Control and helper backups include trusted-root definitions, selection intent,
and helper-owned bindings. Imported content is not included. A restore on a
host with a missing or different root identity marks storage unavailable and
does not start the application. Reassociation requires a newly validated root;
in-use paths cannot change silently.

## AppArmor and privilege boundary

The helper profile adds metadata traversal for `/mnt`, `/media`, `/data`, and
`/srv` directory trees so it can canonicalize and stat registered roots. It
does not grant the helper imported-file read access. Docker performs only the
helper-constructed bind. Existing no-shell, no-arbitrary-path, capability-free,
protected-socket, authentication, CSRF, Origin/Host, and exact-exposure tests
remain in the validation suite.

## Documentation pass

The gstack `document-generate` pass produced or updated the explanation,
how-to, reference, security, catalog, support, troubleshooting, backup, known
limitations, changelog, and release-note surfaces. The subsequent
`document-release` pass cross-checked those surfaces against schema 4, helper
operations, UI fields, and the final diff. The new guide is reachable directly
from README.

## Validation and artifacts

The final validation covered `go test ./...`, `go vet ./...`, gofmt, strict
manifest parsing, storage-path and helper tests, reconciliation, privilege
boundary, package tests, ShellCheck, AppArmor loading, systemd verification,
JSON parsing, documentation links, `git diff --check`, sensitive-data scanning,
and two-build reproducibility.

| Artifact | SHA-256 |
| --- | --- |
| Debian `kitpro-server_0.1.0~alpha10_amd64.deb` | `2c69657190d35c9883d34c6073d6047bed4c3ccf89b660fb4f43ebee50b3c5d4` |
| Arch `kitpro-server-0.1.0_alpha10-1-x86_64.pkg.tar.zst` | `d71497751e59e26d6d59f69bc5eb0bc50fc623208ffeaad32625212065ffd0e2` |
| CycloneDX SBOM | `8a8899af3b9262e37cd6b69bfa6a235891b0c47b356c2467bf21e052c7278f66` |

## Remaining limitations

- One read-only Jellyfin media library is accepted; multiple library slots and
  write-enabled media management are not yet catalog capabilities.
- KITPro registers host-mounted NAS paths but does not manage network mounts,
  credentials, or availability ordering.
- No accepted application uses imported read-write storage yet.
- Syncthing needs typed UDP/multi-service networking without host networking.
- Immich needs dedicated multi-container readiness, database, ML, and photo
  lifecycle acceptance.
- Imported content remains the administrator's backup responsibility.

STORAGE + MEDIA ACCESS ARCHITECTURE: PASS
