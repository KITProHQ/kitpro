# Disposable platform provisioning plan

## Status

The VMs were later operator-provisioned as Proxmox VM 500 (Debian 13) and VM 501 (Rocky Linux 10). The earlier blocked attempt remains preserved in [`results/2026-09-12-platform-provisioning-blocked.md`](results/2026-09-12-platform-provisioning-blocked.md).

Rocky completed a real validation run. Debian stopped at the capacity gate because its 40 GiB disk was partitioned with only 8.4 GiB assigned to `/`. See the dated [Debian](results/2026-09-12-debian13-validation.md) and [Rocky](results/2026-09-12-rocky10-validation.md) records. This document remains the reproducible provisioning plan; it is no longer the current status record.

This plan provisions ordinary upstream Debian and Rocky virtual machines. It does not create KITPro OS media, modify the old KITProOS project, or install production KITPro services.

## Safety boundary

- Use the existing Proxmox node `hermes` only after the operator grants access to the two named validation guests and required datastores.
- Do not modify existing VMs 112 (`ubs26`) or 119 (`omarchy`). They are outside this test.
- Re-query the next free VM ID immediately before each create operation. The observed value `123` was not reserved and must not be treated as available later.
- Refuse creation when either name or VM ID already exists.
- Use no public bridge, router port forward, public address, or Proxmox firewall exception.
- Keep credentials, API tokens, installation passwords, SSH private keys, and raw transcripts containing them out of this repository.
- Never use an unfiltered Docker prune or delete an object that lacks the fixture's exact run identity.

## Access needed

The safest handoff is for a Proxmox administrator to create the two empty VMs and upload the verified ISOs. The existing automation token can then receive rights only on those two VM paths for audit, power, console, snapshot, and rollback operations. Guest access must use an explicitly supplied SSH identity or console credential.

If KITPro automation will create the VMs, use a dedicated Proxmox pool and token. Grant only the VM allocation/configuration rights needed in that pool, image access on the ISO datastore, space allocation on the selected VM datastore, and audit access to the chosen node and bridge. Do not broaden the existing token across unrelated VMs merely to make this test convenient. Review the effective ACL before creation.

Required preflight evidence:

1. `GET /cluster/nextid` returns a collision-free candidate.
2. `GET /cluster/resources?type=vm` contains neither validation name nor chosen ID.
3. The token can see the selected ISO storage, VM storage, node, and bridge.
4. The selected VM storage supports snapshots.
5. The private bridge provides outbound package access but no public inbound forwarding.
6. The guest SSH access method is known before installation starts.

## Upstream installation media

The filenames and hashes below were read from the publishers on 2026-09-12. Re-read the signed publisher checksum file immediately before download. A newer point release requires a new recorded filename and hash, not reuse of these values by assumption.

| Guest | Upstream image | SHA-256 |
| --- | --- | --- |
| Debian | `debian-13.7.0-amd64-netinst.iso` from `https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/` | `a7ef94ac2fb9a7fec454552abd629b7cc9d5155c886165a45649f5ce6167e355` |
| Rocky | `Rocky-10.2-x86_64-minimal.iso` from `https://download.rockylinux.org/pub/rocky/10/isos/x86_64/` | `aac6ac3ce781b91a91ce78463405f66c611a5dca4b3840c79e5e01d97302f6c8` |

Upload only checksum-verified media to Proxmox's ISO storage. Record the storage volume ID used by each VM.

## VM definitions

Allocate the IDs at execution time. Use these fixed names and settings:

| Setting | Debian guest | Rocky guest |
| --- | --- | --- |
| Name | `kitpro-debian13-validation` | `kitpro-rocky10-validation` |
| VM ID | Allocate at create time | Allocate at create time |
| Type | QEMU/KVM full VM | QEMU/KVM full VM |
| Machine and firmware | q35 and OVMF/UEFI | q35 and OVMF/UEFI |
| CPU | 2 vCPU, host CPU model | 2 vCPU, host CPU model |
| Memory | 4096 MiB, no ballooning during tests | 4096 MiB, no ballooning during tests |
| System disk | 40 GiB on snapshot-capable local storage | Approximately 40 GiB on snapshot-capable local storage |
| Disk controller | VirtIO SCSI | VirtIO SCSI |
| Network | One VirtIO NIC on the approved private bridge | One VirtIO NIC on the approved private bridge |
| Public exposure | None | None |
| Start at host boot | Disabled | Disabled |

The Proxmox host reports AVX2, BMI1, BMI2, FMA, MOVBE, and XSAVE, and its glibc loader marks x86-64-v3 as supported. Use the host CPU model so Rocky can receive those features. The guest baseline must still verify the effective capability. Do not infer guest compatibility from the host record alone.

Record the final VM ID, MAC address, storage volume, bridge, firmware, ISO volume, and DHCP-assigned private address in the dated result file.

## Guest installation

Install through the normal upstream installer.

For Debian:

- install Debian 13 amd64 with current security updates;
- use the whole 40 GiB system disk with ext4 and no separate workload-data claim;
- select a minimal server, OpenSSH server, and standard system utilities;
- install `qemu-guest-agent` and enable time synchronization; and
- create only the test administrator needed for the run.

For Rocky:

- install Rocky Linux 10 x86_64 with the Minimal Install environment;
- retain the installer's supported default filesystem unless the run records a reason to change it;
- leave SELinux Enforcing and keep firewalld in its installed default state;
- install `qemu-guest-agent`, enable chronyd, and record guest x86-64-v3 capability; and
- create only the test administrator needed for the run.

Use an operator-approved SSH public key or console credential. Do not place guest passwords in scripts, shell history, transcripts, or result documents.

## Snapshot sequence

Use these snapshot roles, with the runtime-specific second name:

1. `clean-os`: clean updated OS, guest access verified, no KITPro runtime prepared.
2. `docker-installed` on Debian or `podman-installed` on Rocky: host runtime verified, no KITPro application present.
3. `helper-installed`: optional checkpoint after KITPro units and identities exist, before test application objects.
4. `managed-fixture`: optional checkpoint for snapshot-dependent ownership and recovery tests.

Record the Proxmox snapshot identifier and creation time. Restore `clean-os`
between fresh installer runs and the runtime-specific snapshot between
independent groups. A manually repaired guest is not clean-run evidence.

## Baseline and Docker workflow

Copy or clone exact commit `8e8e05176b5f9c2669d702b679e034828bdc08f4` into each guest and verify `git rev-parse HEAD` before execution. Then capture a transcript and run:

```sh
sudo tools/platform-validation/verify-host.sh --platform debian13
sudo tools/platform-validation/prepare-debian.sh \
  --acknowledge-disposable-vm \
  --yes
sudo tools/install-docker.sh --verify-only
```

On Rocky, substitute:

```sh
sudo tools/platform-validation/verify-host.sh --platform rocky10
sudo tools/platform-validation/prepare-rocky.sh \
  --acknowledge-disposable-vm \
  --yes
sudo tools/platform-validation/verify-host.sh \
  --platform rocky10 \
  --require-podman
getenforce
```

Do not use `--grant-user-access`. If a stock minimal image has a conflicting package, record it first and use `--remove-conflicts` only after reviewing the exact package transaction.

Create `podman-installed` only after verification passes, Rocky still reports
`Enforcing`, firewalld is active/enabled, and Docker remains absent.

## Fixture and manual test workflow

Start the Debian Docker fixture from `docker-installed` and follow
[`../../prototypes/privilege-boundary/RUNBOOK.md`](../../prototypes/privilege-boundary/RUNBOOK.md).
The Debian automated command is:

```sh
sudo tools/platform-validation/run-privilege-fixture.sh \
  --platform debian13 \
  --acknowledge-disposable-vm
```

Start Rocky from `podman-installed`, install the reviewed RPMs, then use:

```sh
sudo tools/platform-validation/validate-rocky-runtime.sh \
  --acknowledge-disposable-vm --require-app
```

Complete the runbook's snapshot-dependent label/record disagreement tests, mount-boundary and race cases, interruption case, full reboot, loopback IPv4 and IPv6 publication tests, second-host reachability checks, and final inventory. Do not convert an untested manual row to `PASS` because the automated core completed.

For Rocky, follow
[`rocky-linux-10-validation.md`](rocky-linux-10-validation.md) and
[`../security/selinux-rocky-podman.md`](../security/selinux-rocky-podman.md).
Collect AVC evidence before installation, while applications run, after
service and host restarts, and after cleanup. Stop on a related AVC until the
denied boundary is understood.

## Evidence and cleanup

Create new timestamped copies of the Debian and Rocky templates before each attempt. Hash the raw transcript and record its location without committing secrets. Record every `PASS`, `FAIL`, `BLOCKED`, `NOT RUN`, and `OBSERVATION` separately.

After the fixture removes its resources, prove that unrelated containers, networks, and volumes are unchanged. Stop and disable only the test units. Restore the appropriate snapshot rather than manually cleaning a guest into a claimed baseline. Do not delete the VMs until Josh accepts the result records.
