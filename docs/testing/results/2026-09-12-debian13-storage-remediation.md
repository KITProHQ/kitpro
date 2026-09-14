# Debian 13 storage remediation: 2026-09-12

## Run identity

- Status: `PASS` for storage remediation; Docker validation remains `BLOCKED` pending a new clean snapshot
- VM: Proxmox VM 500, `10.10.0.115`
- OS: Debian GNU/Linux 13.7 (trixie)
- Existing immutable baseline: [blocked Debian validation](2026-09-12-debian13-validation.md)
- Repository baseline: `8e8e05176b5f9c2669d702b679e034828bdc08f4`

The original `clean-os` snapshot was not changed. Docker was not installed before or during this remediation.

## Original layout

| Device | Start sector | Sector count | Size | Filesystem | Mount | Use |
| --- | ---: | ---: | ---: | --- | --- | --- |
| `/dev/sda1` | 2,048 | 18,028,544 | 8.6 GiB | ext4 | `/` | 7.0 GiB free |
| `/dev/sda2` | 18,032,638 | 65,851,394 | 31.4 GiB | DOS extended | n/a | container for logical partitions |
| `/dev/sda5` | 18,032,640 | 4,358,144 | 2.1 GiB | swap | swap | zero bytes used |
| `/dev/sda6` | 22,392,832 | 61,491,200 | 29.3 GiB | ext4 | `/home` | 48 KiB of account data; 27.2 GiB free |

There was no LVM. `/var`, system packages, logs, and future Docker metadata all lived on the undersized root filesystem. The nominal 40 GiB virtual disk therefore did not satisfy the 20 GiB KITPro/system free-capacity requirement.

## Correction

Status: `PASS`.

The VM was disposable and had an existing hypervisor snapshot. Before changing the partition table, the validation preserved `/home/josh`, `/etc/fstab`, and the disk's first MiB on the root filesystem. It then:

1. disabled the unused swap;
2. unmounted the nearly empty separate `/home`;
3. restored the `josh` home and SSH key onto the root filesystem;
4. removed only the obsolete `/home` and swap entries from `fstab`;
5. replaced the DOS partition layout with the same bootable root start sector and an extended root end sector; and
6. rebooted so the kernel consumed the new table and grew ext4 to the partition boundary.

The root partition start remained sector 2,048. No Docker, KITPro fixture, public service, firewall rule, or unrelated VM was touched.

## Corrected baseline

| Check | Status | Observation |
| --- | --- | --- |
| Partition table | PASS | DOS table now contains only bootable `/dev/sda1`, start 2,048, size 83,881,984 sectors. |
| Root filesystem | PASS | ext4, approximately 40 GiB total and 37 GiB / 39,249,825,792 bytes free. |
| KITPro capacity | PASS | The filesystem containing `/var`, system packages, Docker data, and future KITPro state exceeds 20 GiB free. |
| Separate application capacity | OBSERVATION | None is allocated by this VM. Real application data, media, backups, and databases still require separate planning. |
| SSH access | PASS | `josh` key authentication continued after reboot. |
| Validation sudo | PASS | `sudo -n` continued to work; `/etc/sudoers.d/kitpro-validation` was retained. |
| PID 1 | PASS | `systemd`. |
| Cgroups | PASS | Unified cgroup v2 (`cgroup2fs`). |
| Docker cleanliness | PASS | Docker remained absent. |
| Swap | OBSERVATION | The validation VM now has no swap. This is acceptable for the test run but should be an explicit reference-image decision, not an accidental production default. |

## Remaining gate

Docker installation remains `BLOCKED` until Proxmox VM 500 has a new snapshot named `storage-corrected-clean-os`. The original `clean-os` snapshot must remain unchanged because it documents the failed installer layout. The current automation identity cannot create or verify Proxmox snapshots noninteractively.
