# Alpha.14 fresh Debian qualification

Date: 2026-10-08

Status: `FRESH_DEBIAN_PASS_EXTERNAL_GATES_OPEN`

This record qualifies the fresh Debian path below, including the full first-run
workflow. It does not declare alpha.14 release readiness, qualify other platforms
or an alpha.13 upgrade, or authorize final release staging.

## Source and candidate identity

The initial checkout was clean on `develop/0.1.0-alpha.14`, at
`2f3900ac85e3d579fb71456e8e36dee86f8f7a39`, tree
`b447f3b2895c3d1a605e28e9729c541d037286fe`.

Two defects found during live qualification were reproduced, covered by regression
tests, and committed locally:

- `72f8c3b7e5d43db1e6b577133eb2fcb1da6d02e8`: use the same-origin referrer policy
  so browser first-run forms retain their origin. Null, missing, and foreign
  origins remain rejected.
- `d24a0bba414caceece70a624e57de43d85769056`: place restored routes on hold and
  invalidate archived active ingress metadata before exchanging the control
  database; select new ingress generations beyond existing disk history.
  Previously, restore could reuse a generation directory and activate a route
  before its restored backend was ready.

The passing qualification-only package is `kitpro-server_0.1.0~alpha14_amd64.deb`:

- Source commit: `d24a0bba414caceece70a624e57de43d85769056`.
- SHA-256: `2a10f809e0f5aee92664bccd5b5960a45ec27b39c5934f10f0531bebe1772ffe`.
- Package version/architecture: `0.1.0~alpha14`, `amd64`.
- Build metadata: `source_tree_dirty=false`; both extracted binaries embed the
  exact source commit above.
- Local path: `/tmp/kitpro-alpha14-20261008/candidate-d24a0bb/kitpro-server_0.1.0~alpha14_amd64.deb`.

Evidence from candidates built at `2f3900a` and `72f8c3b` is superseded. Each
replacement was installed after restoring this run's clean OS snapshot; the
passing installation was genuinely fresh. A later documentation-only commit does
not change the identity of these tested bytes.

## Verified baseline

The initially restored guest at `10.10.10.112` contained an older alpha.14
installation and 31 containers. It was rejected as a fresh-install baseline and
was not cleaned or modified. The operator subsequently supplied `10.10.10.114`.
Guest-agent network data and SMBIOS identity established that it is VM 500.

| Property | Observed baseline |
| --- | --- |
| Proxmox endpoint/node | `10.10.40.50:8006`, `hermes` |
| VM ID/name/role | 500 / Debian-13.7.0 / KITPro Debian release-test VM |
| SMBIOS UUID | `25f40ea4-0365-4ad3-a763-0d0d35b96b20` |
| Hostname | `debian` |
| Machine ID | `23c90d5cfb074f9ea5264e70c2f54290` |
| OS/kernel | Debian 13.7 / `6.12.111+deb13-amd64` |
| Network | ens18, `BC:24:11:53:7E:3D`, DHCP `10.10.10.114/24`, gateway `10.10.10.1`, vmbr0 VLAN 10 |
| Resources | 2 cores, 4092 MiB RAM, 80 GiB disk; ext4 root 74 GiB, initially 69 GiB free |
| Initial state | Booted, zero failed units; no KITPro, nginx, Docker, managed state, or workloads |
| Initial snapshots | No saved snapshots; synthetic current entry only |
| Qualification snapshot | `clean-os-alpha14-20261008`, created before mutation |

The VM description's reserved `.200` address is stale relative to observed DHCP;
no network configuration was rewritten. Existing Athena SSH identity and repository
Proxmox credentials were used. This fresh OS lacks sudo; existing guest-agent
privilege supplied administrative execution without root SSH or new privilege
configuration. The obsolete snapshot-chain blocker did not apply.

## Passing live gates

| Gate | Evidence |
| --- | --- |
| Fresh package installation | Dependencies installed through the existing disposable-VM preparation helper; package installation, embedded identity, database initialization, ownership, and modes verified. Docker CE 29.8.2 / containerd 2.3.6. |
| Service startup/security | API, helper socket/service, managed nginx and Docker operational; KITPro and Docker AppArmor profiles enforcing; zero failed units and no PID-1 ordering-cycle messages. |
| Ten-step first run | Host, runtime, storage, network, pool, administrator, local access, optional HTTPS, optional Cloudflare, and first application exercised. Browser refresh, interrupted service restart/resume, and reboot persistence passed. Critical steps reject manual completion. HTTPS initially skipped, later becomes complete when a certificate exists; Cloudflare remains explicitly skipped. |
| Docker pool | Missing-pool preflight rejected installation readiness. Administrator explicitly configured `172.30.0.0/16`, child `/24` (256 nominal networks; required minimum 64), validated and restarted Docker. KITPro left configuration unchanged. Temporary conflicting route produced `address_pool_conflict`, current-pool details, remediation, and restart guidance in readiness/UI; removing that test route restored readiness. |
| First application | Memos 0.30.0 installed through the normal browser catalog. Trusted storage registration, initial internal binding, desired/runtime state, typed readiness, restart, stop/start, and normal transition to loopback binding verified. Active generation 2 listens only on `127.0.0.1:20000`; generation 1 is retained and stopped. |
| HTTP ingress | No default route; explicit enablement; HTTP 200; unknown Host rejected; backend inaccessible directly from LAN; stopped backend gives 502; route deletion removes access; recreation and nginx reload succeed. |
| Provided HTTPS | Locally generated qualification certificate imported through the normal provided-certificate API. Hostname/SNI and certificate trust checked for `memos.alpha14.test`; HTTPS 200 survives restore and reboot. This is not ACME evidence. |
| Administrator recovery | Packaged reset command denies non-root execution; root reset accepts the new password, rejects the old password, revokes two existing sessions including the browser, and does not echo plaintext. |
| Support bundle | Live bundle mode 0600 and hash verified; scans exclude known passwords/session/CSRF values, helper secret values, private-key markers, auth headers, and Cloudflare-token patterns. A temporary synthetic sensitive operation makes generation fail closed with no artifact; fixture removed. |
| Host backup/restore | Packaged control and helper restore succeed while services are stopped. Both databases retain restrictive mode 0600 and correct ownership; integrity checks pass. Restored ingress remains disabled with `restore_hold_active`; reconciliation returns 409 while backend is stopped. Hold survives reboot. After normal application start and consistent reconciliation, explicit ingress reconciliation activates generation 7, preserving existing generation directories. |
| Subsequent backup | Synchronous backup succeeds after restore/reactivation; archive checksum, mode 0600, and control database integrity verified. |
| Final reboot | API/helper/nginx/Docker operational; Memos running and consistent; all ten steps complete/skipped; ingress and HTTPS ready; unknown Host rejected; database integrity/ownership/permissions preserved; AppArmor enforcing; failed units 0; genuine ordering cycles 0; service-log secret scan passes. |

A post-start assertion immediately after restore did not pass; its response was
not retained. No duplicate start was submitted. After the held-state reboot,
reconciliation explicitly reported `consistent`, `running`, and recommended
`none`. Final reactivation and a further reboot independently passed. Readiness
reports firewall `unknown` / `firewall_not_inspected`, which accurately describes
the constrained helper's inspection boundary; Cloudflare is disabled by choice.

## Artifact and state references

- Installation: `inst-6dd3a6c099acee22`.
- Restored route: `route-f26f66089e994cde`; certificate: `cert-3aeafde5aa866af2`.
- Support bundle: `bundle-857b99fcbe9b72fc.tar.gz`, SHA-256
  `d10fe7336149a3bea6044276ecf769e93b65a72e93fa7776b34c2ede2553355f`.
- Restore source backup: `op-9ad5173e7f76d90a8080fb0f6712745c`; helper archive
  SHA-256 `be4ffc8adde7d19f708b377efe570634be71220027bce252879d5e2a585dd60a`.
- Subsequent backup: `op-de6b2ae0003924c0384578570dd37027`; helper archive
  SHA-256 `c87f95213e2911309ea077d94ad54b3a733cfef0621f2ab92f6ca446ca8a3229`.
- Final boot ID: `e9e14620-5fef-439c-9277-01fa87e3483d`.
- Docker configuration SHA-256 throughout installation and recovery:
  `0e5b569da4495769a28de13132eacce16da26b464c76ec03054126963a4805f3`.
- Local execution evidence: `/tmp/kitpro-alpha14-20261008/`; temporary evidence
  paths are not durable release artifacts. Secret-bearing backups remain on the
  disposable guest; no backup contents are included in this record.

## Regression validation and remaining blockers

The complete Go suite passed with permission for its local test listeners.
Browser/web tests, Debian static package checks, incoming pre-install tests,
and fail-closed upgrade tests passed. New regression tests cover browser origin
policy, staged restore holds/source preservation, active generation invalidation,
existing generation history, and rejection of restored stopped backends before
helper mutation.

The remaining external gates for this continuation are:

1. Controlled Let's Encrypt staging issuance and renewal, requiring an authorized
   public test hostname and working challenge reachability.
2. Authorized Cloudflare live lifecycle qualification, requiring scoped provider
   credentials and explicitly authorized test resources.

Neither resource set was supplied during this run. Provided-certificate HTTPS
and Cloudflare skipping do not satisfy those gates. Other-platform and alpha.13
upgrade evidence must be assessed separately; this record makes no new claim
about them. No final release artifacts were rebuilt or staged. Nothing was
pushed, tagged, published, or uploaded to a release/distribution service, and no
production deployment occurred. Candidate transfers and installation were limited
to the authorized disposable VM.
