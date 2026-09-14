# Controlled application service exposure — complete acceptance

Date: 2026-09-13

## Result

`CONTROLLED SERVICE EXPOSURE GATE: PASS`

This is a new immutable record. Earlier partial records remain unchanged. It
closes the gate with the production API, helper, Docker Engine, and Debian VM
500 rather than with a serializer-only fixture.

## Production implementation

- FreshRSS declares the `web` HTTP service on container port 80. The manifest
  has no host-address or host-port authority.
- Exposure is persisted per installation and service. The retained assignment
  was `inst-6dbaface46557406` / `web` / port `20000` in the configured
  `20000-29999` range.
- `internal` creates no Docker host binding. `loopback` creates exactly
  `127.0.0.1:20000 -> 80/tcp`. `lan` creates exactly
  `10.10.0.115:20000 -> 80/tcp`; neither mode creates a wildcard binding.
- The helper reloads the embedded catalog and independently validates the
  application, release, image digest, service, protocol, container port,
  exposure mode, policy address, retained port, runtime generation, and
  storage derivation before calling Docker.
- The helper also compares a stopped container's observed binding with trusted
  exposure state before starting it. A drifted container cannot regain network
  exposure through the normal start operation.
- Exposure changes preserve the installation and storage, increment the
  runtime generation, create a new constrained runtime, and verify the
  observed Docker binding before success.
- Disabling exposure retains port 20000 while recreating an internal-only
  runtime. Re-enabling reused port 20000.
- FreshRSS containers now carry the manifest's `unless-stopped` restart policy;
  an integration failure where Docker restart left a container exited exposed
  and fixed the missing Docker `RestartPolicy` field.

## Debian 13.7 measurements

| Case | Result | Evidence |
| --- | --- | --- |
| Internal baseline | PASS | Docker `PortBindings` was empty; same-network HTTP returned 302. |
| Loopback | PASS | Docker and `ss` showed only `127.0.0.1:20000`; loopback HTTP returned 302. |
| Loopback isolation | PASS | `http://10.10.0.115:20000/` returned connection failure while loopback mode was active. |
| LAN | PASS | Docker and `ss` showed only `10.10.0.115:20000`; host-LAN and a separate authorized LAN peer both returned HTTP 302. |
| Wildcard denial | PASS | No `0.0.0.0` or `::` binding appeared; helper tamper tests reject both. |
| Persistent data | PASS | Marker SHA-256 `c97403f75ce801838718600ff3b2788e62686c785baa908cb332bfd5854124b3` was unchanged across internal, loopback, LAN, collision recovery, restart, reboot, and final disable. |
| Stable assignment | PASS | Port 20000 remained stable across runtime recreations, API restart, helper restart, Docker restart, and VM reboot. |
| New-assignment collision | PASS | A foreign listener on candidate 20001 was left untouched; another installation received 20002. |
| Persisted-port collision | PASS | A foreign listener on `10.10.0.115:20000` caused a failed operation, remained alive, produced `exposure_collision`, and did not trigger reassignment. After removal, retry reused 20000. |
| Disable/re-enable | PASS | Disable removed host publication, preserved storage, retained 20000, and kept internal HTTP working; re-enable restored the same port. |
| API/helper restart | PASS | Assignment and authenticated service state persisted; reconciliation stayed exact. |
| Docker restart | PASS | Docker restored the `unless-stopped` FreshRSS runtime with the exact LAN binding and no duplicate resource. |
| VM reboot | PASS | Services returned without manual repair; generation 13, LAN assignment, port 20000, endpoint, and marker survived; AppArmor remained enabled. |
| Wrong observed binding | PASS | A controlled loopback/wrong-port replacement against trusted LAN intent classified `security_drift`; KITPro did not adopt it. |
| Internal intent with publication | PASS | A controlled published replacement against trusted internal intent classified `security_drift`; recovery returned to exact. |
| Authentication | PASS | Anonymous mutation returned 401, missing submitted CSRF proof returned 403, bad Origin returned 403, and client-supplied `host_port` returned 400. Rejected requests created no valid helper mutation. |
| UI | PASS | The installed service view showed Web Interface, protocol, internal port, current mode, and the exact loopback/LAN endpoint; internal mode showed Not exposed. |
| Audit | PASS | Journald contained `exposure_requested`, `exposure_allocated`, `exposure_reused`, `exposure_applied`, `exposure_disabled`, `exposure_failed`, `exposure_collision`, and `exposure_drift_detected`, with installation/service/mode/port context. A token/password/CSRF-value scan was clean. |

The authentication acceptance run found and fixed a generic CSRF defect: the
server previously accepted the CSRF cookie itself when no header or form token
was submitted. Mutations now require an explicit submitted token matching the
server-side session record. A regression test covers cookie-only rejection.

Operation summaries shown by the API/UI now map helper failures to bounded,
user-safe categories. Raw Docker daemon paths and object details remain in the
privileged service journal rather than being copied into new control-plane
operation summaries.

## Firewall observation

Docker 29.8.0 created exact-address listeners and nftables DNAT rules. In
loopback mode, `docker-proxy` listened on `127.0.0.1:20000` and the rule matched
destination `127.0.0.1`. In LAN mode it listened on `10.10.0.115:20000` and the
rule matched that destination before forwarding to the application bridge.
KITPro did not change global firewall policy.

## Final state

FreshRSS was left running in `internal` mode with empty Docker host bindings.
Port 20000 remains reserved to its installation/service, reconciliation is
`exact`, same-network HTTP returns 302, and the persistent marker remains
unchanged. Disposable listeners, drift containers, cookies, and plaintext
validation credentials were removed. No commit or push was performed.

## Final repository validation

- `go test -count=1 ./...`: PASS
- `go vet ./...`: PASS
- `gofmt -l`: PASS with no output
- `git diff --check`: PASS
- Exposure, allocator, helper tamper, Docker request, lifecycle,
  reconciliation, API/UI, and authentication regressions are included in the
  passing Go suite.
- The direct Go dependency files did not change, so the dependency-change
  condition for another pinned `govulncheck` run did not apply.
- The changed-file credential/private-key scan found no secret material.
