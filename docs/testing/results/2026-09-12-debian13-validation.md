# Debian 13 platform validation: 2026-09-12

## Run identity

- Overall status: `BLOCKED`
- VM: Proxmox VM 500, `10.10.0.115`
- Snapshot: `clean-os`
- OS: Debian GNU/Linux 13 (trixie)
- Repository baseline: `8e8e05176b5f9c2669d702b679e034828bdc08f4`
- Fixture revision: not deployed
- Docker installation: not attempted
- Procedure: [`prototypes/privilege-boundary/RUNBOOK.md`](../../../prototypes/privilege-boundary/RUNBOOK.md)

This record preserves the attempted Debian run. It is not a Debian platform-validation result. The root filesystem provides only 8.4 GiB total and 7.0 GiB free, so the host does not meet KITPro's documented minimum of 20 GiB free for system and KITPro capacity. Testing stopped before Docker installation as required.

## Measured baseline

| Item | Status | Observation |
| --- | --- | --- |
| VM identity | PASS | VM 500 at `10.10.0.115` reports Debian GNU/Linux 13. |
| Kernel and architecture | PASS | `6.12.107+deb13-amd64`, `x86_64`. |
| PID 1 | PASS | `systemd`. |
| systemd | PASS | 257.13. |
| cgroup mode | PASS | `cgroup2fs`, unified cgroup v2. |
| CPU and memory | OBSERVATION | 2 vCPU; 3.8 GiB RAM; 2.1 GiB swap. |
| Root filesystem | FAIL | `/dev/sda1`, ext4, 8.4 GiB total, 7.0 GiB / 7,421,435,904 bytes free. |
| Disk layout | OBSERVATION | 40 GiB disk split into 8.6 GiB `/`, 2.1 GiB swap, and 29.3 GiB `/home`. Capacity exists on the virtual disk but is not assigned to the system root. |
| Network | PASS | `ens18` has `10.10.0.115/24`; default route is `10.10.0.1`; no public exposure was added. |
| MAC and firmware | OBSERVATION | `bc:24:11:53:7e:3d`; legacy BIOS boot. |
| Time synchronization | PASS | `systemd-timesyncd` active. |
| AppArmor | OBSERVATION | Service active and enabled; `aa-status` is not installed. No extra security tooling was installed. |
| nftables/iptables tools | OBSERVATION | Neither `nft` nor `iptables` CLI was installed at baseline. |
| Docker baseline | PASS | Docker executable and `docker-ce` package absent; Docker service inactive. |
| Temporary validation sudo rule | OBSERVATION | `/etc/sudoers.d/kitpro-validation` permits the confirmed non-interactive validation access. |

## Blocked test groups

| Test group | Status | Reason |
| --- | --- | --- |
| Docker installer | BLOCKED | System root does not meet the 20 GiB free-capacity prerequisite. |
| `docker-installed` snapshot | NOT RUN | Docker was not installed. |
| Docker socket authority | NOT RUN | Docker socket does not exist. |
| systemd helper fixture | NOT RUN | Fixture was not deployed. |
| Real helper-to-Docker operations | NOT RUN | Docker was not installed. |
| Ownership disagreement matrix | NOT RUN | No Docker resources were created. |
| Filesystem security fixture | NOT RUN | The run stopped at the host-capacity gate. |
| Docker networking | NOT RUN | No Docker resources were created. |
| Restart/recovery and VM reboot | NOT RUN | No fixture state existed. |
| Fixture cleanup | NOT RUN | No fixture resource was created. |

## Unblock condition

Josh must correct the Debian storage layout so the system/KITPro filesystem has at least 20 GiB free, then restore or create a clean baseline snapshot. Application data, media, photos, backups, and databases need separate additional capacity planning and do not count toward this minimum.

Do not continue from a manually altered partial Docker installation. Docker was not installed during this attempt, so the current `clean-os` snapshot remains the only recorded baseline.
