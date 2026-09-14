# Platform provisioning attempt: 2026-09-12

## Run identity

- Overall status: BLOCKED
- Repository revision: `8e8e05176b5f9c2669d702b679e034828bdc08f4`
- Observation completed: `2026-09-12T22:10:05Z`
- Intended Debian VM: `kitpro-debian13-validation`
- Intended Rocky VM: `kitpro-rocky10-validation`
- Procedure: [`docs/testing/platform-provisioning-plan.md`](../platform-provisioning-plan.md)

No target VM was created. No existing VM, Proxmox configuration, local hypervisor configuration, firewall, package set, user, container, or snapshot was changed.

## Virtualization inventory

| Item | Status | Observation |
| --- | --- | --- |
| Configured Proxmox access | PASS | Existing SSH alias `proxmox` reached node `hermes`; Proxmox VE reported version 9.2.11. |
| Proxmox API authentication | PASS | An existing development token completed read-only version, node, resource, permission, next-ID, network, and VM-config requests. No token value was printed or stored. |
| Proxmox node state | OBSERVATION | One node, `hermes`, reported online with cgroup mode 2. |
| Existing VM protection | PASS | VMs 112 (`ubs26`) and 119 (`omarchy`) were identified as unrelated and left unchanged. |
| Candidate VM ID | OBSERVATION | `/cluster/nextid` returned `123`. It was not reserved and cannot be assumed free later. |
| New-VM permission | BLOCKED | Effective permissions existed only under `/vms/112` and `/vms/119`; `/vms/120` returned no rights. The token lacks authority to allocate either validation VM. |
| Storage visibility | BLOCKED | The token returned an empty storage inventory, so ISO upload and VM-disk allocation cannot be validated or performed. |
| Proxmox private bridge | BLOCKED | The visible node network data exposed physical interfaces but no bridge. A Proxmox administrator must identify the approved private bridge before guest creation. |
| Non-interactive Proxmox shell administration | BLOCKED | The configured SSH account works but does not have non-interactive sudo. No password was requested or guessed. |
| Local libvirt session | OBSERVATION | `qemu:///session` is reachable and contains no guests, pools, or networks. `/dev/kvm` is available. |
| Local provisioning toolchain | BLOCKED | `qemu-system-x86_64`, `qemu-img`, `virt-install`, and `cloud-localds` are unavailable. No host package installation was authorized or attempted. |
| Local installation media | BLOCKED | Neither Debian 13 nor Rocky Linux 10 target media is present. Existing unrelated ISO files were not used. |

## Media observations

| Publisher record read on 2026-09-12 | Status | Observation |
| --- | --- | --- |
| Debian current amd64 netinst | OBSERVATION | `debian-13.7.0-amd64-netinst.iso`, SHA-256 `a7ef94ac2fb9a7fec454552abd629b7cc9d5155c886165a45649f5ce6167e355` |
| Rocky 10 current x86_64 minimal | OBSERVATION | `Rocky-10.2-x86_64-minimal.iso`, SHA-256 `aac6ac3ce781b91a91ce78463405f66c611a5dca4b3840c79e5e01d97302f6c8` |
| Media download/upload | NOT RUN | No ISO was downloaded or uploaded. Recheck publisher signatures and hashes before a later run. |

## Rocky CPU feasibility observation

The Proxmox host reports an Intel Xeon E-2226G with AVX, AVX2, BMI1, BMI2, FMA, MOVBE, and XSAVE. Its glibc loader reports `x86-64-v3 (supported, searched)`. This is host-only evidence. The future Rocky VM must use an appropriate CPU model and verify the effective capability inside the guest before the platform test can pass.

## Platform test status

| Test group | Debian 13 | Rocky Linux 10 | Reason |
| --- | --- | --- | --- |
| VM provisioning | BLOCKED | BLOCKED | No authorized allocation/storage path |
| Clean OS installation | NOT RUN | NOT RUN | VMs do not exist |
| `clean-os` snapshot | NOT RUN | NOT RUN | VMs do not exist |
| Baseline validation | NOT RUN | NOT RUN | No guest environment |
| Docker installation | NOT RUN | NOT RUN | No guest environment |
| `docker-installed` snapshot | NOT RUN | NOT RUN | Docker not installed on target guests |
| Docker authority tests | NOT RUN | NOT RUN | No guest Docker socket or test identities |
| Real privilege-boundary fixture | NOT RUN | NOT RUN | No guest Docker Engine |
| Ownership disagreement tests | NOT RUN | NOT RUN | No fixture Docker resources |
| Filesystem tests | NOT RUN | NOT RUN | No target guest filesystem |
| Networking and firewalld tests | NOT RUN | NOT RUN | No target guest network |
| SELinux and AVC review | Not applicable to Debian acceptance | NOT RUN | No Rocky guest |
| Restart, reboot, and recovery | NOT RUN | NOT RUN | No target services or state |
| Cleanup validation | NOT RUN | NOT RUN | No test resources were created |

## Exact unblock condition

Use [`docs/testing/platform-provisioning-plan.md`](../platform-provisioning-plan.md). Provisioning can resume when either:

1. a Proxmox administrator creates the two named empty guests, uploads verified upstream media, and grants the existing automation identity narrowly scoped access to those guest paths and required snapshots; or
2. Josh explicitly authorizes a dedicated Proxmox pool/token with the VM and datastore rights described in the plan.

The handoff must also provide an explicit guest console or SSH access method. Do not broaden access to VMs 112 or 119, and do not treat ID 123 as reserved.
