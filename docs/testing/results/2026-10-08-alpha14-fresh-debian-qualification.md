# Alpha.14 fresh Debian qualification

Date: 2026-10-08

Status: `FRESH_DEBIAN_PASS / BLOCKED_EXTERNAL_TEST_RESOURCES`

VM 500 is the authoritative Debian qualification host for this record. Its
Debian operating system was wiped and reinstalled clean before alpha.14
qualification began. There was no preexisting KITPro installation, Docker
runtime, managed state, or unrelated workload on that fresh baseline. The
current KITPro state was created by this qualification run.

A snapshot is recovery tooling, not proof of whether an operating system was
freshly installed. The fresh baseline was established by the rebuild and guest
inspection. A convenience snapshot named `clean-os-alpha14-20261008` was also
created before qualification mutations; its presence or absence would not
change the fresh-install conclusion.

This record qualifies Debian-local alpha.14 behavior. It does not declare final
release readiness, qualify other operating systems, or authorize publication
staging.

## Source and candidate identity

Before qualification the checkout was clean on
`develop/0.1.0-alpha.14`, at
`94397d06aefbc8e6cdbf302697dc5d91ff716dcb`, with tree
`7f73852045f8cedaa1d93275f70d84118fde6992`.

The qualification package was rebuilt locally from that exact clean source:

| Property | Value |
| --- | --- |
| Filename | `kitpro-server_0.1.0~alpha14_amd64.deb` |
| Local path | `/tmp/kitpro-alpha14-20261008/candidate-94397d0/kitpro-server_0.1.0~alpha14_amd64.deb` |
| SHA-256 | `19e3e2b3b5e4f87a4dfca1431810cd3345ced22d623723f86b6aaf7b0e7ae9df` |
| Version/architecture | `0.1.0~alpha14` / `amd64` |
| Embedded source commit | `94397d06aefbc8e6cdbf302697dc5d91ff716dcb` |
| Clean-source marker | `source_tree_dirty=false` |
| Build time | `2026-10-08T21:37:56Z` |

Both extracted binaries and the installed API reported the expected commit.
This is a qualification candidate, not a final release artifact.

Earlier live qualification exposed two defects that are already included in
the current source and have regression coverage:

- `72f8c3b7e5d43db1e6b577133eb2fcb1da6d02e8` retained the same-origin
  referrer needed by browser first-run forms while continuing to reject null,
  missing, and foreign origins.
- `d24a0bba414caceece70a624e57de43d85769056` held restored routes inactive,
  invalidated archived active ingress metadata before control-database
  exchange, and allocated new ingress generations beyond existing disk
  history.

No additional product defect was found during the original qualification of the
exact `94397d0` candidate. A later correction to the external-gate architecture
identified a Cloudflare-origin defect; its fix and affected-gate evidence are
recorded below without rewriting the identity of the original fresh-install
candidate.

## Fresh host identity and baseline

Proxmox API inventory and guest-agent inspection agreed on the following
identity:

| Property | Observed state |
| --- | --- |
| Proxmox endpoint/node | `10.10.40.50:8006` / `hermes` |
| VM ID/name | 500 / `Debian-13.7.0` |
| Hostname | `debian` |
| Guest address | `10.10.10.114/24`, gateway `10.10.10.1` |
| MAC/network | `BC:24:11:53:7E:3D`, `vmbr0`, VLAN tag 10 |
| SMBIOS UUID | `25f40ea4-0365-4ad3-a763-0d0d35b96b20` |
| Machine ID | `23c90d5cfb074f9ea5264e70c2f54290` |
| OS/kernel | Debian GNU/Linux 13.7 / `6.12.111+deb13-amd64` |
| Resources | 2 cores, 4092 MiB RAM, 80 GiB virtual disk |
| Baseline storage | ext4 root 74 GiB, initially about 69 GiB free |
| Baseline services | zero failed units; AppArmor enabled |
| Baseline workload | no KITPro package, Docker, nginx, KITPro state, containers, or unrelated workload |

The VM description's older reserved-address note did not match observed DHCP;
no guest networking was rewritten. Access used the established Athena SSH
identity and repository Proxmox credentials. The clean Debian image did not
include `sudo`; privileged guest work used the existing Proxmox guest-agent
path without root SSH or passwordless-sudo configuration.

## Package, runtime, and host-security result

- The exact candidate installed successfully from the restored clean OS
  snapshot, with package dependencies satisfied.
- Docker CE 29.9.0, containerd 2.4.1, and Compose 5.6.0 were installed by the
  existing disposable-host preparation path.
- `kitpro-api.service`, `kitpro-helper.socket`, `kitpro-ingress.service`,
  `kitpro-acme.service`, `kitpro-cloudflare.service`, and
  `kitpro-ingress-validator.service` started and survived reboot.
- The control and helper databases initialized, passed SQLite integrity checks,
  retained the expected owners, and remained mode `0600`.
- The final boot had zero failed units and no PID-1 ordering-cycle message.
- Service logs contained no tested password, authorization, cookie, CSRF, or
  private-key material.
- AppArmor remained enabled and enforcing for the KITPro helper, ACME,
  Cloudflare, ingress validator, and `docker-default` profiles. Application
  lifecycle, certificates, diagnostics, backup, restore, ingress, and reboot
  were exercised without an AppArmor denial.
- Final root usage was 1.9 GiB of 74 GiB, with 68 GiB available.

## Ten-step first run and Docker address pool

All ten durable first-run steps were exercised: host readiness, runtime,
storage, network, Docker address pool, administrator, local access mode, HTTPS,
optional Cloudflare, and first application. Critical steps rejected attempts to
mark them complete without their real prerequisites. Browser interruption and
refresh, API/helper restart, and a real VM reboot preserved the state. The final
state was nine complete steps plus Cloudflare explicitly skipped. Local and LAN
operation continued without Cloudflare.

The missing-pool prerequisite initially blocked readiness. The operator then
explicitly configured `172.30.0.0/16` with `/24` child networks, providing 256
nominal networks against the 64-network requirement, and restarted Docker.
KITPro did not rewrite `/etc/docker/daemon.json`; its SHA-256 remained
`0e5b569da4495769a28de13132eacce16da26b464c76ec03054126963a4805f3`.
A temporary conflicting route produced `address_pool_conflict`, current-pool
details, remediation, and `requires_docker_restart=true`. Removing the fixture
returned readiness to `address_pool_sufficient`.

## Application lifecycle and managed ingress

Memos 0.30.0 installation `inst-123ed256328609bc` passed generated storage,
secret generation, initial internal-only binding, desired/runtime state, typed
readiness, stop, start, restart, and recreation. The active runtime is
generation 3 on loopback port 20000; the verified previous generation is
retained stopped as designed.

A second Memos installation, `inst-48a9c7677070b608`, was used to prove routing
isolation. Each hostname routed only to its own installation; stopping either
backend produced 502 for that host while the other continued to return 200.
The second route was removed and its active runtime was removed while stored
application data and the normal retained prior generation were preserved.
Final reconciliation reports the primary installation `consistent/running` and
the second `consistent/runtime_removed`.

Managed nginx passed:

- no route merely because an application was installed;
- explicit enablement and route removal;
- strict hostname validation, duplicate rejection, and unknown-Host rejection;
- backend isolation and no cross-installation routing;
- stopped-backend 502 and recovery after start;
- candidate validation, reload, route generation, and reboot persistence.

For failure injection, an invalid generation 999 failed `nginx -t`. Temporarily
pointing `current` at that candidate and signaling nginx left its master alive
and the previous configuration serving. The symlink was restored, reload
succeeded, HTTPS remained available, and the qualification-only invalid
directory was removed. Restore later held ingress until valid target state and
activated generation 8 only after explicit reconciliation.

## Provided-certificate HTTPS

The provided-certificate path created certificate
`cert-5f878a9ae7b76851` for `memos.alpha14.test` and route
`route-cde24d7e0f5a69e5`. Hostname validation, PEM validation, key matching,
certificate/hostname matching, atomic activation, and HTTPS 200 passed.
Malformed PEM, a mismatched key, an invalid hostname, and a hostname/certificate
mismatch were rejected while the known-good certificate remained active.

The full chain and private key are root-owned mode `0600`. APIs expose
metadata only. The private key was absent from tested service logs and support
bundles. After the final reboot, HTTPS still returned 200 and an unknown host
was rejected.

## ACME and Cloudflare local regression

The ACME scheduler was active after reboot. Local API checks rejected an
unapproved directory and invalid hostname. A controlled failed issuance for
`acme.alpha14.test` recorded `issuance_failed` without a leaf certificate while
the known-good provided certificate and HTTPS route remained active. Focused
ACME tests passed directory allowlisting, scheduler and renewal transitions,
failed issuance/renewal behavior, and known-good certificate retention. The
Let's Encrypt directory allowlist was not weakened.

Cloudflare remained disabled with no configured provider. Setup could skip it,
local operation continued, and no credentials were invented or used. Focused
tests passed write-only credential handling, enable/disable state transitions,
failure behavior, API projection, and diagnostics redaction. The packaged
Cloudflare service was active after reboot even though provider readiness was
correctly `disabled/provider_disabled`.

## Cloudflare-first ACME correction and affected candidate

This qualification environment is behind an ISP that blocks inbound TCP/80.
Direct public HTTP-01 access to VM 500 is neither available nor required, and
its absence is not an infrastructure misconfiguration. The required external
sequence is Cloudflare Tunnel first, followed by public HTTP and ACME HTTP-01
through the tunnel to KITPro's managed ingress challenge handler. Router port
forwarding and direct public exposure of VM 500 are outside this qualification
path.

Implementation review reproduced a product defect in the original renderer.
Cloudflare routes target KITPro's loopback origin at `127.0.0.1:18080`, but the
hostname server on that listener sent the ACME challenge path to the application
backend. The listener also had no explicit rejecting default server, allowing
nginx to select the first configured tunnel hostname for an unmatched Host.

Commit `4b12da0a807bce03d287e078b8c3aebb756486c5` adds the existing bounded
ACME challenge location to each authorized Cloudflare hostname and adds a
single default server on the loopback tunnel listener that returns 444. The
regression failed before the fix and passed afterward. Full Go tests, Go vet,
UI tests, and Debian, Arch, and RPM package static tests passed.

The affected Debian candidate was rebuilt from the clean fix commit and
installed over the earlier alpha.14 candidate on VM 500:

| Property | Value |
| --- | --- |
| Filename | `kitpro-server_0.1.0~alpha14_amd64.deb` |
| SHA-256 | `9b7251196954d8e115d386697e998c4dab45af771342ee375d1e77a2c612dd12` |
| Source commit | `4b12da0a807bce03d287e078b8c3aebb756486c5` |
| Source tree | `ec291fc436ab2ed8783fcb6e13da4e2e088ce3d5` |
| Clean-source marker | `source_tree_dirty=false` |

An isolated live nginx instance on VM 500 validated the fixed generated
configuration without changing active ingress: the authorized tunnel hostname
reached its intended backend with HTTP 200, a nonexistent ACME token reached
the challenge handler and returned 404, and an unknown Host was rejected with
nginx's 444 behavior. The installed candidate then passed service, database,
AppArmor, active HTTPS, unknown-Host, Docker-pool, application-runtime, failed
unit, ordering-cycle, and secret-log checks.

This is local proof of the corrected origin contract. It is not live proof of
Cloudflare edge routing or Let's Encrypt staging. Those gates still require the
authorized disposable resources below.

## Administrator recovery and diagnostics

The packaged recovery command rejected non-root SSH execution. The established
privileged guest path reset the administrator password, revoked two active
sessions, invalidated the old browser/API sessions, rejected the old password,
and accepted the new password. Session state was updated; neither password was
present in service logs or any control/helper database value.

The final post-reboot support bundle was
`bundle-50904ba1f1c3e455.tar.gz`, SHA-256
`6928700e0ddae6587550d214855a8965e1b03f5c014d3d423d4f8e789056cc3d`.
It was mode `0600` and contained the expected safe policy, runtime, and
readiness information. Scans excluded known application/helper secrets,
passwords, Cloudflare tokens, authorization headers, cookies, CSRF material,
and private-key markers. A temporary synthetic sensitive operation caused
bundle generation to fail closed with HTTP 500 and no completed artifact; the
fixture was removed.

## Backup, offline restore, and reboot

Backup `op-af4d2cf1fd7747f8384c6fec26c7c04a` produced a mode-`0600`
control database and helper archive. The helper archive SHA-256 was
`e86bd3ce3ba92379132bdae5cd611cbdd30abb0d488cc5c15f349bb831017b12`.
Integrity and contents checks covered the control database, helper database,
ingress metadata, provided-certificate files, ACME local state, Cloudflare local
credential state as designed, and setup state. Metadata explicitly reports
`remote_cloudflare_resources_included=false`.

The offline restore stopped the relevant services, checkpointed SQLite WAL,
verified the archive, restored both databases with correct ownership and mode
`0600`, and retained restrictive secret-file permissions. A durable restore
journal and rolled-back generation records were present. Ingress entered
`restore_hold_active`, remained unavailable before backend reconciliation, and
activated only after the restored application was consistent. The restored host
then produced subsequent backup `op-02de410766751c934957d4f4c81e12ae`;
its helper archive SHA-256 was
`94274a0dda31c7a58450b21d45c2d9ad8d30ded5eac27c3fa23c5f8ccf4ed5fe`.

A real Proxmox reboot produced boot ID
`cf653531-1291-43d8-84df-5ebc0627bb9e`. The VM retained the same UUID, MAC,
VLAN, address, package identity, databases, application state, setup state,
certificate, route, Docker pool, and AppArmor policy. Zero failed units, no
genuine ordering cycle, primary application readiness, generation 8 ingress,
HTTPS, diagnostics generation, and secret scans all passed after reboot.

Interrupted-restore behavior and journal recovery were rerun in focused
automated regression. A second live process kill was not introduced after the
successful offline restore because the deterministic test-hook coverage proves
the interruption boundary without leaving the authoritative guest in ambiguous
state. Helper and API restart, stopped backend, invalid certificate replacement,
invalid nginx candidate, and nginx activation failure were all injected live.

## Repository regression

The following passed against the exact source checkout:

- `go test ./...` (outside the restricted sandbox so loopback test servers could
  bind);
- `go vet ./...`;
- `node --test cmd/kitpro-api/web_test.mjs`;
- Debian, Arch, and RPM package static tests;
- Debian incoming pre-install tests using the published alpha.12 binaries;
- Debian and Arch fail-closed upgrade-gate tests;
- fresh focused ACME, Cloudflare, diagnostics, host-backup, backup,
  authentication, certificate, ingress, API, and helper tests;
- `git diff --check`.

The first sandboxed Go run and two package-test attempts could not create local
listeners or their default `/var/tmp` Go cache. They passed unchanged when run
with the required loopback permission and a writable qualification cache under
`/tmp`; these were runner restrictions, not product failures.

## Result and external blockers

The following Debian-local gates are complete:

- Debian 13 fresh installation;
- ten-step first run;
- AppArmor live enforcement;
- application lifecycle;
- managed ingress and cross-installation isolation;
- provided-certificate HTTPS;
- administrator recovery;
- diagnostics and redaction;
- backup and offline restore;
- reboot and recovery.

Two external-resource gates remain `BLOCKED_EXTERNAL_TEST_RESOURCES` and must
run in this order:

1. Live Cloudflare lifecycle qualification needs explicitly authorized scoped
   credentials, zone/account metadata, a disposable hostname, and a disposable
   tunnel or permission to create and delete one.
2. After that tunnel publishes KITPro's loopback HTTP origin, live Let's Encrypt
   staging issuance and renewal need the same authorized hostname, contact
   email, and subscriber-terms authorization. HTTP-01 must traverse Cloudflare
   Tunnel; direct inbound TCP/80 to VM 500 is not part of the gate.

The post-qualification local resource discovery was repeated before stopping:

- KITPro's ignored `.env` contains only the established Proxmox API and guest
  privilege inputs. It does not provide an ACME hostname, contact email,
  Cloudflare tunnel routing declaration, or authorization to accept the
  staging subscriber terms.
- No local ACME client state or explicitly authorized non-production FQDN was
  present, so public DNS, tunneled HTTP-01 issuance, and renewal could not be
  exercised safely.
- No valid scoped Cloudflare token file, insufficient-scope token file,
  replacement token file, account/zone/test-host metadata, or disposable
  tunnel authorization was present.
- A separate homelab environment contains production home-tunnel credential
  material and a repository example contains a placeholder token. Neither is
  an authorized disposable alpha.14 test resource, and neither was read into
  KITPro or used for an API call.

No ACME account, order, challenge, certificate, DNS record, Cloudflare route,
or tunnel was created, modified, or deleted during this discovery pass.

Those missing resources do not invalidate the successful fresh-Debian
qualification. No final release artifact was frozen, and publication staging
was not run. Nothing was pushed, tagged, published, uploaded, or deployed to a
production system.
