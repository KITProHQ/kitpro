# Rocky Linux 10 Podman preflight, 2026-09-15

## Status

`BLOCKED` before installation or mutation.

The repository-recorded disposable target, VM 501 at `10.10.0.114`, is still
reachable. It is not a clean native-runtime baseline, so it was not used for a
KITPro package build or Podman acceptance run.

## Read-only observations

| Item | Result |
| --- | --- |
| Host identity | `localhost.localdomain` |
| Distribution | Rocky Linux 10.2 (Red Quartz) |
| Architecture | x86_64, inherited from the prior result record |
| SELinux | `Enforcing` |
| firewalld | `active` |
| Podman | No executable found in the login environment |
| Docker | `/usr/bin/docker` exists |

The preflight used noninteractive SSH and read only host identity, OS release,
SELinux state, firewalld state, and runtime executable paths. It did not install
packages, stop services, change firewall or SELinux configuration, restore a
snapshot, or touch KITPro state.

## Blocker

The required matrix starts with a clean Rocky 10 VM with no Docker and the
native Podman stack. Restoring VM 501's `clean-os` snapshot would destroy its
current state, and no verified snapshot-control path was available in this
session. Installing Podman beside the existing Docker experiment would not
produce clean-host evidence.

## Not run

- RPM build and reproducibility
- Podman and crun version capture
- Quadlet generator validation
- fresh KITPro installation
- application, storage, DNS, and port behavior
- container SELinux labels and AVC window
- reboot and failure recovery
- upgrade
- remove/reinstall and purge

Rocky must remain experimental. Resume from a confirmed disposable `clean-os`
snapshot or a newly provisioned clean Rocky Linux 10 VM, then use the current
[`Rocky validation checklist`](../rocky-linux-10-validation.md).
