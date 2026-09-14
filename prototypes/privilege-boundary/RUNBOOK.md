# Disposable reference-VM runbook

Use this procedure only on a snapshotted, clean Debian 13 amd64 or Rocky Linux 10 amd64 VM. It creates test users, systemd units, root-owned test state, one internal Docker network, and disposable containers. It must not run on the current Arch workstation or a machine with valuable workloads.

Record every command, exit status, and relevant output in a copy of the matching template under [`docs/testing/results/`](../../docs/testing/results/).

## 1. Verify and snapshot the VM

Before changing the guest, record:

```sh
cat /etc/os-release
uname -srmo
ps -p 1 -o comm=
stat -fc %T /sys/fs/cgroup
findmnt -no FSTYPE,OPTIONS /
free -h
df -h /
```

Expected: the selected distribution, `systemd`, `cgroup2fs`, amd64 or `x86_64`, and the planned filesystem and capacity. Create a hypervisor snapshot named `clean-os` before Docker installation and `docker-installed` after Docker passes verification.

On Rocky, also record:

```sh
getenforce
sestatus
```

Stop if SELinux is not `Enforcing`. Do not change enforcement.

## 2. Install test prerequisites

Use the repository's distribution adapter to install Docker Engine from Docker's official repository. This keeps package and repository behavior outside the helper protocol:

```sh
# Debian 13
sudo tools/platform-validation/prepare-debian.sh \
  --acknowledge-disposable-vm \
  --yes

# Rocky Linux 10
sudo tools/platform-validation/prepare-rocky.sh \
  --acknowledge-disposable-vm \
  --yes
```

Run only the command for the current guest. If the script reports conflicting packages, review them before adding `--remove-conflicts`. Neither preparation script grants Docker group access, changes firewall policy, or changes SELinux mode. The Rocky path uses Docker's RHEL repository as a compatibility target; it is not a claim that Docker certifies Rocky Linux.

Install the distribution's `python3` package. The fixture has no third-party Python dependency. Confirm Docker uses a local Unix socket and is healthy:

```sh
docker version
stat -c '%A %a %U %G %n' /var/run/docker.sock
```

Do not enable a Docker TCP listener.

The safe core sequence in sections 3 through 7 and the basic helper and Docker restart checks can be run with:

```sh
sudo tools/platform-validation/run-privilege-fixture.sh \
  --platform debian13 \
  --acknowledge-disposable-vm
```

Use `--platform rocky10` on Rocky. This runner refuses pre-existing fixture state and test-labelled resources. It does not replace the snapshot-dependent ownership disagreement tests in section 8, specialized mount/race cases in section 6, second-host network exposure tests, arbitrary interruption testing, or final SELinux review.

## 3. Create disposable identities and files

Debian identity setup:

```sh
groupadd --system kitpro-pb-api-test
useradd --system --gid kitpro-pb-api-test --home-dir /nonexistent --shell /usr/sbin/nologin kitpro-pb-api-test
```

Rocky identity setup:

```sh
groupadd --system kitpro-pb-api-test
useradd --system --gid kitpro-pb-api-test --home-dir /nonexistent --shell /sbin/nologin kitpro-pb-api-test
```

Do not add this user to `docker`, `wheel`, or any other privileged group.

From a reviewed checkout of this repository, stage the fixture. Replace `/path/to/kitpro` with the checkout path:

```sh
install -d -o root -g root -m 0755 /opt/kitpro-privilege-boundary-test
install -d -o root -g root -m 0755 /opt/kitpro-privilege-boundary-test/fixture
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/fixture/*.py /opt/kitpro-privilege-boundary-test/fixture/
install -d -o root -g root -m 0755 /opt/kitpro-privilege-boundary-test/tests
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/tests/*.py /opt/kitpro-privilege-boundary-test/tests/
cp /path/to/kitpro/prototypes/privilege-boundary/README.md /opt/kitpro-privilege-boundary-test/
cp /path/to/kitpro/prototypes/privilege-boundary/PROTOCOL.md /opt/kitpro-privilege-boundary-test/
chown -R root:root /opt/kitpro-privilege-boundary-test
install -d -o root -g root -m 0700 /var/lib/kitpro-pb-test
python3 -c 'import uuid; print(uuid.uuid4())' | install -o root -g root -m 0600 /dev/stdin /var/lib/kitpro-pb-test/run-id
```

Use `busybox:1.37.0` only to discover a small test image, then record and deploy its immutable digest:

```sh
docker pull --platform linux/amd64 docker.io/library/busybox:1.37.0
docker image inspect --format '{{json .RepoDigests}} {{.Os}}/{{.Architecture}}' docker.io/library/busybox:1.37.0
```

Review the output. Write the selected value in the exact form `docker.io/library/busybox@sha256:<64 lowercase hex characters>`:

```sh
install -o root -g root -m 0600 /dev/stdin /var/lib/kitpro-pb-test/image
```

Type the fully qualified digest, press Enter, then send end-of-file. Do not put a mutable tag in this file. Record the registry, repository, discovery tag, platform, and digest in the result log.

## 4. Install and inspect the systemd fixture

Use `/usr/local/lib/systemd/system` so the test files remain separate from distribution packages:

```sh
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/systemd/kitpro-pb-test.socket /usr/local/lib/systemd/system/
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/systemd/kitpro-pb-test.service /usr/local/lib/systemd/system/
install -d -o root -g root -m 0755 /usr/local/lib/tmpfiles.d
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/systemd/kitpro-pb-test.tmpfiles /usr/local/lib/tmpfiles.d/kitpro-pb-test.conf
systemd-analyze verify /usr/local/lib/systemd/system/kitpro-pb-test.socket /usr/local/lib/systemd/system/kitpro-pb-test.service
systemd-tmpfiles --create /usr/local/lib/tmpfiles.d/kitpro-pb-test.conf
systemctl daemon-reload
systemctl enable --now kitpro-pb-test.socket
systemctl status kitpro-pb-test.socket --no-pager
stat -c '%A %a %U %G %n' /run/kitpro-pb-test /run/kitpro-pb-test/helper.sock
```

Expected directory: `root:kitpro-pb-api-test`, mode `0750`. Expected socket: `root:kitpro-pb-api-test`, mode `0660`.

On Rocky, record labels before the first request:

```sh
ls -ldZ /opt/kitpro-privilege-boundary-test /var/lib/kitpro-pb-test /run/kitpro-pb-test
ls -lZ /run/kitpro-pb-test/helper.sock /var/run/docker.sock
```

Do not run `chcon`, `setenforce`, or `audit2allow`.

## 5. Verify caller identity and direct Docker denial

Verify the API-test user has no Docker group:

```sh
id kitpro-pb-api-test
runuser -u kitpro-pb-api-test -- python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect("/var/run/docker.sock")'
```

The second command must fail with permission denied. If it connects, stop: the privilege boundary is invalid.

An unrelated account must fail at the helper socket's filesystem permissions. Use an existing nonprivileged account that is not in `kitpro-pb-api-test`:

```sh
runuser -u nobody -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock ping
```

From `/opt/kitpro-privilege-boundary-test`, the allowed identity must succeed:

```sh
cd /opt/kitpro-privilege-boundary-test
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock ping
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock inspect-runtime
systemctl status kitpro-pb-test.service --no-pager
journalctl -u kitpro-pb-test.service --since '5 minutes ago' --no-pager
```

The ping result must show the API-test UID from `SO_PEERCRED` and `human_authorization_verified: false`.

## 6. Run protocol and filesystem tests

Run the repository-only suite first:

```sh
cd /opt/kitpro-privilege-boundary-test
python3 -m unittest discover -s tests -v
```

Then exercise rejection through the root helper:

```sh
runuser -u kitpro-pb-api-test -- python3 -m fixture.negative_probe --socket /run/kitpro-pb-test/helper.sock
```

Every probe must report `accepted: false`. The helper must remain active afterward.

For mount substitution, create a second disposable filesystem mounted below a separate test root, record its device and mount ID, and call only the directory fixture against it. Do not mount over `/var/lib/kitpro-pb-test` while the helper is running. A high-frequency rename or mount race needs a separate harness; do not claim it passed based on the deterministic symlink-replacement test.

## 7. Run the Docker lifecycle

All client calls use only the semantic instance `demo`:

```sh
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock create demo
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock inspect demo
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock start demo
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock stop demo
```

Inspect the test objects as the VM administrator:

```sh
docker ps -a --filter label=invalid.kitpro.privilege-boundary-test.managed=true --no-trunc
docker network ls --filter label=invalid.kitpro.privilege-boundary-test.managed=true --no-trunc
docker inspect $(docker ps -aq --filter label=invalid.kitpro.privilege-boundary-test.instance=demo)
docker network inspect $(docker network ls -q --filter label=invalid.kitpro.privilege-boundary-test.instance=demo)
```

Verify the digest, labels, non-root user, read-only root filesystem, capability drop, `no-new-privileges`, lack of mounts and devices, exactly one internal test network, and empty port bindings. No host listener should appear because the fixture cannot request a published port.

Create an unrelated stopped container with no fixture labels:

```sh
docker create --name kitpro-pb-unrelated --network none $(cat /var/lib/kitpro-pb-test/image)
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock stop unrelated
docker inspect --format '{{.Name}} {{.State.Status}} {{json .Config.Labels}}' kitpro-pb-unrelated
```

The helper request must return `OwnershipUnproven`, and the unrelated container must remain unchanged. The protocol offers no way to submit its Docker ID.

## 8. Exercise ownership disagreements

Take a VM snapshot before each case and restore it afterward. Stop the helper service, but leave the socket unit installed, before using `fixture.state_tool`. Pass `--acknowledge-helper-stopped` on every invocation.

Use the following fault patterns, then return to socket activation and request `inspect demo`, `stop demo`, or `remove demo` with a fresh operation ID. Do not start the helper directly: it intentionally requires one inherited systemd socket. If repeated fault cases hit systemd's start-rate limit, reset only these disposable units and restart the socket before the next client request:

```sh
systemctl reset-failed kitpro-pb-test.service kitpro-pb-test.socket
systemctl start kitpro-pb-test.socket
```

| Case | Fault injection | Required result |
| --- | --- | --- |
| Expected labels missing | Point the container record at a safe, stopped, unlabeled test container with `replace-record` | `OwnershipUnproven`; no Docker mutation |
| Helper record missing | Remove the container record with `remove-record` while its labeled object remains | `OwnershipUnproven` or `OwnershipConflict`; no mutation |
| Incorrect instance ID | Request a different valid semantic instance | `OwnershipUnproven`; original unchanged |
| Incorrect resource type | Change the protected record type with `wrong-record-kind` | `OwnershipUnproven`; no mutation |
| Unknown Docker object ID | Replace a record with a nonexistent ID | `OwnershipUnproven`; no mutation |
| Label-only resource | Remove the record, then retry create while the labeled object remains | `OwnershipConflict`; no duplicate or deletion |
| Helper-record-only resource | Remove a known test object directly through Docker while retaining its record | `OwnershipUnproven`; no other object touched |

Example state-tool prefix:

```sh
python3 -m fixture.state_tool --state-file /var/lib/kitpro-pb-test/state.json --run-id-file /var/lib/kitpro-pb-test/run-id --image-file /var/lib/kitpro-pb-test/image --acknowledge-helper-stopped show
```

Only pass object IDs that were created for this disposable fixture. Never point a fault-injection record at an unrelated host resource outside a reverted test snapshot.

## 9. Restart and recovery

Use a fixed UUID for an initial ping, repeat it from a new client, restart the helper, and repeat it again:

```sh
python3 -c 'import uuid; print(uuid.uuid4())'
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock --operation-id REPLACE_WITH_UUID ping
systemctl restart kitpro-pb-test.service
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock --operation-id REPLACE_WITH_UUID ping
```

The result must be replayed from the root-owned receipt. Reuse that operation ID with a different operation and confirm `OperationConflict`. Query a random UUID with `get-operation` and confirm `NotFound`.

Restart Docker, confirm a runtime inspection reports a bounded failure during the outage, then confirm it recovers after Docker is active. Repeat inspection after client exit and reconnect. Do not infer rollback from a lost connection.

An interrupted Docker mutation and automated step reconciliation are not implemented. Killing the helper at an arbitrary point may leave a `running` receipt and a partial resource set; the required fixture result is `RecoveryRequired`, followed by manual inspection. Record this limitation rather than deleting unknown state.

## 10. Rocky SELinux evidence

Record before and after:

```sh
getenforce
ps -eZ | grep kitpro-pb-test
ls -ldZ /opt/kitpro-privilege-boundary-test /var/lib/kitpro-pb-test /run/kitpro-pb-test
ls -lZ /run/kitpro-pb-test/helper.sock /var/run/docker.sock
docker inspect --format '{{.ProcessLabel}} {{.MountLabel}}' $(docker ps -aq --filter label=invalid.kitpro.privilege-boundary-test.instance=demo)
docker info --format '{{json .SecurityOptions}}'
container_pid=$(docker inspect --format '{{.State.Pid}}' $(docker ps -q --filter label=invalid.kitpro.privilege-boundary-test.instance=demo))
ps -p "$container_pid" -o label=,pid=,comm=,args=
ausearch -m AVC,USER_AVC -ts recent
```

If Docker omits `name=selinux`, reports empty process/mount labels, or the container runs as `spc_t`, record a failed confinement expectation. `getenforce=Enforcing` and an empty AVC search do not prove the workload is confined.

If an AVC appears, identify the source domain, target type, object class, and denied permission. Stop the affected case and document the smallest policy requirement. Do not disable SELinux, use a broad permissive domain, or install unreviewed `audit2allow` output.

## 11. Cleanup and revert

First use the helper to remove the owned test instance:

```sh
runuser -u kitpro-pb-api-test -- python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock remove demo
docker rm kitpro-pb-unrelated
```

Inspect remaining resources by the exact test label. Do not use an unfiltered Docker prune. Stop and disable the test units, then revert the VM to `docker-installed` for another fixture group or `clean-os` for another installer run. Reverting the snapshot is the authoritative cleanup and removes test identities, files, packages, and state.

If snapshot restore is intentionally deferred, manually remove only the exact fixed fixture paths, units, test identity, digest, and run-labelled objects after copying evidence off-host. Verify the final container, network, and volume inventories. Manual cleanup is not a substitute for a clean snapshot when claiming a fresh rerun.
