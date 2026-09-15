# Media and data-heavy catalog expansion validation

Date: 2026-09-15

Gate: **PASS**

## Candidate decisions

| Candidate | Decision | Evidence and boundary |
| --- | --- | --- |
| Immich 3.2.0 | REJECT FOR CURRENT MODEL | The official topology is server, machine learning, Valkey 9, and PostgreSQL 14 with VectorChord. Safe admission needs shared component secrets, health-gated dependency readiness, bounded 128 MiB database shared memory, and database-aware backup/rollback. KITPro does not replace that topology with SQLite or a partial stack. |
| Navidrome 0.64.0 | ACCEPT | Official amd64 image, HTTP 4533, managed `/data`, imported read-only `/music`, and fixed non-root `1000:1000`. |
| Audiobookshelf 2.36.0 | ACCEPT | Official amd64 image, HTTP 80, managed local `/config` and `/metadata`, imported read-only `/audiobooks`, and fixed non-root `1000:1000`. |
| File Browser 2.63.23 | REJECT FOR CURRENT MODEL | The original upstream repository is archived and states that no future security fixes are planned. |
| SFTPGo 2.7.5 | ACCEPT | Maintained official image, browser-first administrator setup with no default credentials, HTTP 8080, internal SFTP 2022/TCP, managed config, and exclusive imported read-write data. |
| Syncthing | REJECT FOR CURRENT MODEL | It needs TCP 22000, UDP/QUIC 22000, and UDP discovery 21027; upstream documents that Docker bridge mode prevents correct LAN IP discovery. Typed UDP alone would not make the topology supported, and host networking remains prohibited. |
| Jellyfin NVIDIA transcoding | SAFELY DEFERRED | Storage/CPU operation remains supported. Live certification waits for a typed NVIDIA video-capability profile and representative synthetic transcode fixture; KITPro does not claim that generic compute certification proves NVENC/NVDEC. |

## Immutable releases

| App | Official image and linux/amd64 digest | License |
| --- | --- | --- |
| Navidrome | `docker.io/deluan/navidrome@sha256:1a64cbb2603cec5d2615c3a27e91442436b2229583408a55cdc8d85705b95e65` | GPL-3.0 |
| Audiobookshelf | `ghcr.io/advplyr/audiobookshelf@sha256:e388e90e381ae3fa8660346612b2955f2c555ede81c9c286e2218bdf966b4de8` | GPL-3.0 |
| SFTPGo | `ghcr.io/drakkan/sftpgo@sha256:d819bcea946470940416b63604f820aee965a02127b07126785e279fa311258e` | AGPL-3.0-only |

## Generic capability and threat review

Manifest schema v5 adds only a numeric primary runtime UID/GID and matching
managed-storage owner. The API cannot select either value. The helper reloads
the embedded manifest, compares the exact identity, creates only the computed
managed directory, applies its owner, and reconciles Docker `Config.User`.
Supplementary groups remain unavailable.

The helper's capability bounding set contains only `CAP_CHOWN`. AppArmor grants
that operation in `/srv/kitpro` while imported roots remain metadata-only.
Package tests reject `CAP_SYS_ADMIN`, `CAP_DAC_OVERRIDE`, `CAP_DAC_READ_SEARCH`,
and `CAP_MKNOD`. Raw binds, raw users, arbitrary groups, host networking,
privileged mode, and arbitrary devices remain unrepresentable.

Writable roots use one writer exclusively. Two readers may share a read-only
root. A writer conflicts with every other installation binding, including a
reader, and a reader conflicts with an existing writer.

## Debian primary acceptance

Debian 13 VM 500 upgraded to `0.1.0~alpha11` with schema-7 pre-upgrade backups
and migrations. Authenticated root registration and catalog installation passed
for all three accepted apps. Docker inspection proved exact pinned image IDs,
`1000:1000`, no host port publication, per-installation networks, and only the
declared binds.

- Navidrome returned HTTP 302; `/music` was `ro`; an in-container write failed
  with `Read-only file system`.
- Audiobookshelf returned HTTP 200; `/audiobooks` was `ro`; its SQLite state was
  created in managed `/config`.
- SFTPGo returned HTTP 302 to first-run setup; create, rename, and delete worked
  inside its selected root and no other host root was mounted.
- `/`, `/etc`, `/dev`, the Docker socket alias, and a symlink to `/etc` were all
  rejected with HTTP 400.
- A second SFTPGo writer operation completed as failed and created no runtime.
- Recreation advanced all runtimes from generation 1 to generation 2. Synthetic
  managed-state markers survived and reconciliation returned `exact` for each.
- Renaming the music root produced `security_drift`; restoring the same inode
  returned reconciliation to `exact`.
- After VM reboot, API/helper were active, all three generation-2 containers
  returned, and all managed-state markers remained.

Representative existing FreshRSS, Paperless-ngx, Open WebUI, Ollama, and
IT-Tools containers remained running during the expanded-catalog acceptance.

## Ubuntu and Arch smoke

Ubuntu 26.04 VM 502 installed the Debian artifact. Navidrome and SFTPGo installed
through authenticated root selection. Docker showed `1000:1000`, no published
ports, exact read-only/read-write binds, and both services recovered after
reboot.

Arch VM 503 under the existing linux-lts/AppArmor boundary installed
`0.1.0_alpha11-1`. The same authenticated Navidrome/SFTPGo smoke showed exact
identity, mounts, private exposure, and service recovery after reboot.

## Storage, NAS, restore, and update results

Local ext4 identity loss/recovery, recreation, reboot, exclusive writer policy,
and package upgrade migration passed. Control-plane upgrade backups retain root
definitions and bindings; restore on a different identity remains fail-closed
by the existing root identity checks. No disposable NFS/CIFS share was available,
so no live NAS disconnect drill is claimed. Imported libraries are never implied
to be included in control-plane backups.

No application release A-to-B pair was added merely to manufacture an update
test. The package upgrade from alpha.10 state to alpha.11 preserved existing
applications, trusted roots, and helper/control state.

## Validation and artifacts

`go test ./...`, `go vet ./...`, format verification, manifest/storage/helper/
reconciliation tests, package static tests, ShellCheck, AppArmor packaging,
JSON validation, documentation links, `git diff --check`, sensitive-data scan,
and byte-for-byte reproducibility completed successfully.

| Artifact | SHA-256 |
| --- | --- |
| `kitpro-server_0.1.0~alpha11_amd64.deb` | `9af3b958996e9f1bcffee91879e837c02c651d4d02b738cba8c851a124c5cda2` |
| `kitpro-server-0.1.0_alpha11-1-x86_64.pkg.tar.zst` | `1c020fa7d81ce114a3c712be7a954fc5351bdcc0340deefd4694e3c1d6794ed5` |
| `kitpro-server_0.1.0~alpha11_amd64.cdx.json` | `965d2e81a718db3ede458899012b3ec4f0a66489a6f9012d7bc3727717276849` |

## Remaining limitations

- Immich awaits the bounded dependency/readiness, shared-secret, shared-memory,
  and database backup primitives above.
- Syncthing awaits a safe supported LAN transport model, not merely UDP syntax.
- NVIDIA Jellyfin transcoding is not certified by this milestone.
- KITPro does not manage NAS mounts, storage quotas, or imported-data backups.
- One active writer per root is deliberate; writer/reader coexistence is rejected.

**MEDIA + DATA-HEAVY APPLICATION CATALOG EXPANSION: PASS**
