# Alpha.14 supported-platform and upgrade qualification

Date: 2026-10-28

Status: `PLATFORM_GATES_PASS / PUBLIC_ACME_NOT_EXERCISED_NETWORK_LIMITATION / FINAL_ARTIFACTS_PENDING`

This result continues the scope-corrected Debian qualification recorded in
[`2026-10-08-alpha14-fresh-debian-qualification.md`](2026-10-08-alpha14-fresh-debian-qualification.md).
It qualifies Ubuntu 26.04 LTS, Arch Linux, the Rocky Linux 10 Experimental
boundary, and the actual published-alpha.13 package transitions. It does not
publish, upload, tag, push, deploy, or declare final release artifact bytes.

## Source and candidate identity

The qualification started from branch `develop/0.1.0-alpha.14` at
`c06dc5c408c63520a9cfd33870fb6f1caba1554a`, tree
`f17ae1db998dcfaa78544325b172e349e64078c9`, with a clean working tree.

Three packaging defects were fixed and committed locally:

- `aebf20bc10ab885e4c013d5aee817207176ab20e` retires the deferred
  Cloudflare runtime during Arch upgrades;
- `8f3f91b78d20c679b68628e1e75532c6a6c33156` retires the deferred
  Cloudflare runtime, account, files, and SELinux module during RPM upgrades;
- `fb735bcb0ad8db14ebd45f50d7464904afd4972b` terminates an orphaned
  Cloudflare process whose unit file had already disappeared during an RPM
  transaction.

The Supported-platform published-upgrade rerun used clean candidates built
from `fb735bcb0ad8db14ebd45f50d7464904afd4972b`:

| Platform | Candidate | SHA-256 |
| --- | --- | --- |
| Debian and Ubuntu | `kitpro-server_0.1.0~alpha14_amd64.deb` | `7a53366fc4f3e7363f6eb8bee894fc10f1d260c1a550b57302aedc70fc53548c` |
| Arch | `kitpro-server-0.1.0_alpha14-1-x86_64.pkg.tar.zst` | `3bdd1cad3f8087468b1d504cd47c868193c7ea8b56e9ed60a3b0b9c1ed4dc52f` |

These are qualification candidates, not final frozen artifacts.

## Local credential and SSH housekeeping

The operator explicitly waived rotation of the protected qualification guest
privilege credential. No credential value was committed or added to evidence.
The existing protected local configuration remained mode-restricted, and guest
privilege access was successfully exercised on every qualification VM.

On the real Athena host,
`/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf` was owned by `root:root` with
mode `0644`, consistent with a system SSH configuration fragment. Standard SSH
worked without `ssh -F /dev/null`. The restricted runner presented a synthetic
unsafe-permission view, so later runner-local qualification commands used an
explicit empty configuration only as a runner workaround. No host SSH behavior
or configuration file was changed.

## Debian 13 Supported status

The existing live qualification remains `PASS`. The qualified Debian package
was built from clean product source
`68403cd9bcf0c6d88293a0b677b95c1fdeeb13f7`; its SHA-256 is
`128b435547b6617bbc21ceb70082c1e315f4c01f7ff5c38cbe8baa77277d2120`.
Later Arch/RPM packaging corrections do not alter the Debian-qualified product
behavior. The fresh-install, nine-step setup, enforcing AppArmor, Docker pool,
application lifecycle, isolation, ingress, provided-certificate HTTPS,
rollback, recovery, diagnostics, backup/restore, WAL/SHM, reboot, failed-unit,
and ordering-cycle evidence is preserved unchanged.

The actual published-alpha.13 Debian transition was rerun separately against
the clean `fb735bc` candidate. The control and helper databases migrated from
schema 14 to 18. Three application records, the external-storage selection,
the one-way generated-secret fingerprint, and application-backup checksum were
identical before and after the upgrade. Ingress routes, certificates, and
provider routes remained zero. AppArmor stayed enabled, core services stayed
active, failed units remained zero, and no Cloudflare process, unit, or file
appeared.

## Ubuntu 26.04 LTS Supported gate

Result: `PASS` on Ubuntu 26.04.1 LTS.

The live Supported gate used the clean candidate
`kitpro-server_0.1.0~alpha14_amd64.deb`, SHA-256
`497422bdf5f9f720f648a223c4c33d143e2c73a413fd3a18274f38f5ace55036`,
built from `aebf20bc10ab885e4c013d5aee817207176ab20e`. Package installation,
incoming preflight, database verification, backups, and schema 18 migration
passed. The deferred Cloudflare unit, process, account, binary, and AppArmor
profile were removed.

The nine-step setup completed with the approved qualification storage root.
The preserved Open WebUI installation reconciled and served on its loopback
binding. Managed HTTP redirected to HTTPS, the provided certificate served
`ubuntu-alpha14.test`, unknown hosts were rejected, duplicate hostnames were
rejected, and a mismatched certificate could not replace the working route.
Stop, start, restart, and reconciliation behaved as expected.

Administrator recovery revoked the active session and rejected the old
password. The post-reboot support bundle
`bundle-6e504c787bf22e9f` had SHA-256
`b5f15291fb2ea06b294082fabe7d05bbf339f7bf718e8c13b5a2d4974e9f9aa4`,
mode `0600`, and passed the secret-marker scan. Host backup, offline restore,
restore hold, reconciliation, subsequent backup, and a real reboot passed.
AppArmor remained enforcing, relevant denials were zero, failed units were
zero, and systemd verification found no genuine ordering cycle.

The actual published-alpha.13 Ubuntu transition was then rerun against the
clean `fb735bc` candidate. Schema 14 migrated to 18. Application identities,
the generated-secret fingerprint, external-storage selection, and backup hash
were identical before and after. No ingress route, certificate, provider
route, or automatic exposure was created. AppArmor stayed enabled, services
stayed active, failed units remained zero, and no Cloudflare artifact appeared.

## Arch Linux Supported gate

Result: `PASS` on current Arch Linux with `linux-lts`.

The live gate used
`kitpro-server-0.1.0_alpha14-1-x86_64.pkg.tar.zst`, SHA-256
`172e1a31b32236abbef401e5f29ade70c5f4791c8099e82e367ae156246c849a`,
built from `aebf20bc10ab885e4c013d5aee817207176ab20e`. Native package
transactions, hooks, migration, systemd services, AppArmor, Docker readiness,
and the nine-step setup passed. The deferred Cloudflare process, unit, account,
binary, and profile were absent after the upgrade.

Two Memos installations proved lifecycle and cross-installation isolation.
The provided certificate served `arch-alpha14.test`; HTTP redirected, HTTPS
served successfully, unknown hosts were rejected, duplicate hostnames were
rejected, and a mismatched certificate did not replace the working route.
Administrator recovery revoked the active session and rejected the old
credential. Diagnostics redaction, host backup, offline restore, ingress hold,
reconciliation, subsequent backup, and a real reboot passed. The post-reboot
support bundle `bundle-c4c5d8d054f94186` had SHA-256
`39551bd02c6189583abc0c68d750449533bdaa13a6200009a23aa840fbf7cfbf`.
AppArmor remained enforcing, relevant denials were zero, failed units were
zero, and no genuine systemd ordering cycle was present.

The actual published-alpha.13 Arch transition used the published package and
the package-bound `fb735bc` wrapper/candidate. Both databases migrated from
schema 14 to 18, the preserved IT-Tools installation identity, desired state,
release, and runtime generation were unchanged, and no ingress, certificate,
provider route, or automatic exposure was created. AppArmor stayed enabled,
failed units remained zero, and no Cloudflare unit or file appeared.

## Rocky Linux 10 Experimental gate

Result: `PASS` for the documented Experimental regression boundary on Rocky
Linux 10.2. Rocky remains Experimental.

The exact candidates built from `fb735bcb0ad8db14ebd45f50d7464904afd4972b`
were:

| Artifact | SHA-256 |
| --- | --- |
| `kitpro-server-0.1.0~alpha14-1.el10.x86_64.rpm` | `d501346dbd7927c73eb4d03593745f7912f004201571f0c6db3df59103fa28f8` |
| `kitpro-selinux-0.1.0~alpha14-1.el10.x86_64.rpm` | `f9f61014406bdca424c2245174f7ddc239daab4cd4fa2fabe324d9eb06e6f5ff` |
| `kitpro-server-0.1.0~alpha14-1.el10.src.rpm` | `495c3c444ba08c9da257c078c8eb9fac4b40a65c6455b53979dcc6d04194cce9` |

RPM installation/reinstallation, database verification, transition backups,
and schema 18 migration passed. SELinux remained `Enforcing`; the loaded KITPro
module remained present; the deferred Cloudflare module, account, group, unit,
files, and exact executable process were absent. Relevant boot AVCs were zero.
The API, helper socket, ingress, ACME, validator, and reload path recovered
after a real reboot with boot ID
`15355b91-d46b-4e41-9911-19a77645c981`; failed units were zero.

Administrator recovery succeeded. Support bundle
`bundle-c5cf616174c334ef` had SHA-256
`a9162496b313af6b2af0b495810e6c6b836a8f087dbdee6b5e7cc551ba0b7dc2`,
mode `0600`, and passed the extracted secret-marker scan. The existing IT-Tools
installation remained `runtime_removed`, generation zero. This is the already
documented Podman staged-generation lifecycle limitation; no rewrite was
attempted and Rocky was not promoted.

## Public ACME policy determination

Live public Let's Encrypt staging issuance is not an explicit alpha.14
release-blocking proof. The authoritative list in
[`alpha14-secure-access.md`](../../architecture/alpha14-secure-access.md#release-blocking-proofs)
requires migration safety, hostname isolation, transactional rollback,
certificate rejection, redaction, administrator recovery, restore safety, the
three Supported-host gates, and the Rocky Experimental gate. It does not list
successful public issuance. The release notes and known-limitations documents
explicitly state that HTTP-01 requires publicly reachable inbound TCP/80 and
that hosts without it may use administrator-provided certificates.

Implementation and regression tests passed, including directory allowlisting,
state transitions, scheduler behavior, renewal behavior, failure preservation,
and bounded challenge routing. Local HTTP-01 routing passed live. Public
issuance was not exercised because the Cox qualification network cannot accept
direct inbound TCP/80. No Cloudflare workaround was introduced and the ACME
directory allowlist was not weakened. This environmental limitation does not,
by repository policy, block alpha.14 by itself.

## Published alpha.13 artifact verification

Every actual GitHub alpha.13 asset was downloaded from tag
`v0.1.0-alpha.13` and matched the authoritative
[`release-manifest.json`](../../release/release-manifest.json), including the
Debian package and upgrade wrapper, Arch package and wrapper, RPM server and
SELinux packages, and source RPM. No locally substituted alpha.13 package was
used.

The live Debian, Ubuntu, and Arch transitions described above passed. State and
application records remained present, generated secrets and external-storage
state remained unchanged where present, backup metadata remained valid,
ingress remained opt-in, legacy provider state remained inert, Cloudflare was
not exposed, and no application gained external exposure automatically.

## Repository regression at product source

The following passed at
`fb735bcb0ad8db14ebd45f50d7464904afd4972b`:

- `go test -p 1 ./...`;
- `go vet ./...`;
- `node --test cmd/kitpro-api/web_test.mjs`;
- Debian, Arch, and RPM static package tests;
- Debian incoming-preinstall tests using the published alpha.12 binaries;
- Debian and Arch fail-closed upgrade-gate tests;
- publication-staging tests;
- `git diff --check`.

The Go suite contains the focused security, ingress, ACME, diagnostics,
backup/restore, and migration coverage. The successful run used an
NVMe-backed writable Go workspace under `/mnt/kit_archive`; `/tmp` was quota
constrained, `/dev/shm` is intentionally rejected as a protected storage path,
and an initial spinning-disk run was stopped after SQLite tests stalled on I/O.
The unchanged suite then passed sequentially on the policy-valid NVMe path.

## Current result

All platform and published-upgrade gates required before final source freeze
are complete. Final documentation commit, regression against the resulting
exact HEAD, source freeze, final artifact build, exact-byte smoke testing, SBOM
and manifest verification, and local publication staging remain pending. No
push, tag, publication, upload, release creation, announcement, or deployment
occurred.
