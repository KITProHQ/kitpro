# Development-host fixture result: 2026-09-12

## Scope

This is a repository-only fixture result. It is not a Debian or Rocky acceptance result. No Docker API request was made, no container or network was created, and no service, user, group, firewall rule, SELinux setting, or host configuration was changed.

## Environment observations

| Item | Status | Actual observation |
| --- | --- | --- |
| Distribution | OBSERVATION | Arch Linux development host |
| Kernel | OBSERVATION | `7.1.8-arch1-3`, `x86_64` |
| PID 1 visible to task | OBSERVATION | validation sandbox, not systemd |
| systemd tools | OBSERVATION | systemd 261 available for static unit verification |
| cgroups | OBSERVATION | `cgroup2fs` at `/sys/fs/cgroup` |
| Workspace filesystem | OBSERVATION | ext4 |
| Python | OBSERVATION | 3.14.7 |
| SELinux tooling | OBSERVATION | `getenforce` not installed; no SELinux claim possible |
| Docker socket | OBSERVATION | `/var/run/docker.sock`, mode `0660`, shown as `root:nobody` by the sandbox mapping |
| Current UID and GID | OBSERVATION | UID 1000, GID 1000 |
| Current account file-mode check | OBSERVATION | `test -r` and `test -w` both returned success for the mapped Docker socket |
| Docker Engine connection | NOT RUN | The validation sandbox blocks the socket. The account and mapping are not the intended API-test identity, so this cannot validate the boundary. |
| Reference VMs | OBSERVATION | A read-only `virsh list --all` returned no local guests. Debian and Rocky runs remain `NOT RUN`. |
| Docker signing keys | OBSERVATION | Docker's current Debian/Ubuntu primary fingerprint was observed as `9DC858229FC7DD38854AE2D88D81803C0EBFCD88`; its RHEL primary fingerprint was `060A61C51B558A7F742B77AAC52FEB6B621E9F35`. No host keyring changed. |

The successful read/write mode checks are a warning: service identity and supplementary groups must be verified from the actual unit credentials. A sandbox denial cannot compensate for overly broad Unix permissions.

## Commands actually run

```sh
python3 -m unittest discover -s tests -v
systemd-analyze verify prototypes/privilege-boundary/systemd/kitpro-pb-test.socket prototypes/privilege-boundary/systemd/kitpro-pb-test.service
bash -n tools/install-docker.sh tools/tests/test-install-docker.sh tools/platform-validation/*.sh
shellcheck tools/install-docker.sh tools/tests/test-install-docker.sh tools/platform-validation/*.sh
bash tools/tests/test-install-docker.sh
tools/install-docker.sh --dry-run
tools/platform-validation/verify-host.sh --platform debian13
tools/platform-validation/verify-host.sh --platform rocky10
virsh list --all
uname -r
python3 --version
id -u
id -g
ps -p 1 -o comm=
stat -fc %T /sys/fs/cgroup
findmnt -no FSTYPE /home/josh/Documents/GitHub/kitpro
stat -c '%A %a %U %G %n' /var/run/docker.sock
```

The test suite and systemd verifier were rerun outside the restrictive command sandbox. They remained local, used temporary files, and did not contact Docker.

## Test results

| Area | Status | Result |
| --- | --- | --- |
| Python compilation | PASS | Fixture and test modules compiled. |
| Unit and local integration suite | PASS | 35 tests passed in 0.616 seconds. |
| Length framing | PASS | Round trip and 1 MiB oversize rejection ran. |
| JSON validation | PASS | Malformed JSON, duplicate keys, unknown fields, unknown operations, revisions, IDs, and deadlines were exercised. |
| Invalid encoding and JSON shape | PASS | Invalid UTF-8, arrays, and `null` were rejected as malformed messages. |
| Docker security-field rejection | PASS | Requests carrying privileged mode, host network, host PID, host IPC, socket or host mounts, devices, capabilities, security options, sysctls, Docker arguments, or commands failed before service dispatch. |
| Real local `SO_PEERCRED` | PASS | Kernel-reported PID, UID, and GID matched the client process. |
| Unauthorized configured UID | PASS | The connection was rejected before parsing or service dispatch when its peer UID did not equal the configured UID. |
| Socket mode in standalone test | PASS | The temporary socket was mode `0600`. Root ownership and group mode require the VM systemd test. |
| Disconnected client | PASS | Partial-frame disconnect did not terminate the helper; the next request completed. |
| Concurrent clients | PASS | 24 requests completed through a pool of 12 clients. |
| Concurrent mutations | PASS | Eight simultaneous create requests produced one fake container and one fake network. The fixture uses one conservative mutation lock. |
| Client restart | PASS | Each request used a new connection and completed. |
| Helper restart | PASS | A completed operation receipt was replayed after subprocess restart. |
| Duplicate operation ID, same content | PASS | Existing result returned without a second resource creation in the fake Docker test. |
| Duplicate operation ID, different content | PASS | `OperationConflict`. |
| Interrupted receipt | PASS | A persisted `running` receipt returned `RecoveryRequired` after state-store restart. No Docker-step reconciliation is implemented. |
| Unknown operation receipt | PASS | `GetOperation` returned `NotFound` for an unknown UUID. |
| Ownership agreement | PASS | Fixed lifecycle succeeded against a fake Docker boundary only. |
| Missing label | PASS | `OwnershipUnproven`; no mutation. |
| Missing helper record | PASS | Label-only create returned `OwnershipConflict`; lifecycle request returned `OwnershipUnproven`. |
| Incorrect instance | PASS | `OwnershipUnproven`; original fake object unchanged. |
| Incorrect resource type | PASS | `OwnershipUnproven`; no mutation. |
| Unknown Docker object ID | PASS | Record-only target returned `OwnershipUnproven`. |
| Label-only resource | PASS | `OwnershipConflict`; no duplicate or deletion. |
| Helper-record-only resource | PASS | `OwnershipUnproven`; no other object touched. |
| Unrelated container | PASS | Semantic request could not name its Docker ID; fake unrelated object remained running. |
| Extra network attachment | PASS | `PolicyDenied`; lifecycle mutation did not run. |
| Foreign member on instance network | PASS | `PolicyDenied`; lifecycle mutation did not run. |
| Preflight before removal | PASS | A network ownership mismatch prevented deletion of both fake resources. |
| Fixed safe Docker request | PASS | Builder emitted no bind, device, added capability, host namespace, or port; it dropped all capabilities, used a read-only root and non-root UID, and required the internal network. This did not contact Docker. |
| Audit event shape | PASS | Start and completion events came from the helper policy layer and omitted request parameters. The fixture has no secret-bearing operation, so secret-marker coverage remains NOT RUN. |
| Traversal and absolute paths | PASS | Invalid identifiers were rejected and nothing appeared outside the temporary root. |
| Symlink escape | PASS | `openat2` with beneath/no-link/no-cross-mount policy rejected it. |
| Symlink replacement | PASS | Replacing the prepared directory with a symlink caused a fail-closed rejection on the next operation. |
| Static systemd unit verification | PASS | `systemd-analyze verify` exited 0. The units were not installed or started. |
| Installer and validation script syntax | PASS | `bash -n` exited 0 for all new shell scripts. |
| Installer and validation script lint | PASS | `shellcheck` exited 0 after the final test-harness correction. |
| Installer unit tests | PASS | 22 cases passed: all required IDs/versions mapped to the intended repository family; Debian and RHEL command plans named the correct repositories and package set; old, unknown, unsafe-codename, unknown-option, and ambiguous, root, or unsafe-name grant cases failed closed. Synthetic `/etc/os-release` files and command shims were temporary logic tests, not platform emulation. |
| Unsupported host rejection | PASS | The installer rejected Arch instead of inferring support; both reference-host checks rejected Arch before mutation. |
| Public repository key observation | PASS | Three public keys were downloaded to a temporary directory and their primary fingerprints matched the constants recorded by the installer. This did not test target package-manager import. |
| ADR-0017 unchanged | PASS | SHA-256 remained `12987a7395db96100adcf8af549d1906af0d4bd32e5bd8336fbc7d07ab3cb47b`. |

## Not run here

All Docker Engine operations, Docker labels on real objects, direct API-user Docker denial, systemd socket activation, root ownership, group access, Docker restart, host restart, real network isolation, port reachability, mount substitution, high-frequency path races, container interruption, Debian packaging, Rocky packaging, SELinux contexts, and AVC checks are `NOT RUN` on this host.
