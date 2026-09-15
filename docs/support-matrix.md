# Platform support matrix

| Platform | Status | Boundary |
| --- | --- | --- |
| Debian 13 amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Ubuntu 26.04 LTS amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Arch Linux x86_64 | Supported | `linux-lts`, fully updated official repositories, rootful Docker, enforcing AppArmor; no partial upgrades |
| Rocky Linux 10 amd64 | Experimental | Validate SELinux and Docker integration before use |

## Hardware acceleration

| Platform | Discovery | Ollama CPU | NVIDIA | AMD | Intel/VAAPI |
|---|---|---|---|---|---|
| Debian 13 amd64 | Validated | Validated | Validated: driver 550.163.01 and NVIDIA Container Toolkit 1.20.0 | Architecture only | Architecture only |
| Ubuntu 26.04 LTS amd64 | Validated | Validated | Validated: driver 580.178.04 and NVIDIA Container Toolkit 1.20.0 | Unvalidated | Unvalidated |
| Arch Linux x86_64 | Validated | Validated | Validated: `linux-lts`, open DKMS driver 615.71.09, and NVIDIA Container Toolkit 1.20.0 | Device scoping validated locally; compute unvalidated | Device scoping validated locally; accelerated workload unvalidated |

GPU support is not universal. It requires visible vendor hardware, its host driver, a suitable render/compute node, and vendor runtime integration where applicable. QXL virtual graphics does not qualify.
The validated NVIDIA device was an RTX A2000 12GB passed through exclusively to
one test VM at a time. AMD compute and Intel acceleration are not certified.

Ollama is the only shipped GPU-aware application in this release. No additional candidate passed the complete immutable-image, immutable-backend, bounded-storage, typed-device, and network-security review. This is an intentional catalog boundary, not an implication that every NVIDIA container is supported.

## Trusted external storage

| Platform | Local trusted roots | Existing NFS/SMB mount registration | Jellyfin |
| --- | --- | --- | --- |
| Debian 13 amd64 | Validated | Filesystem detection implemented; disposable live NAS unavailable | Validated with read-only media, drift, recreation, and reboot |
| Ubuntu 26.04 LTS amd64 | Validated smoke | Same host-mounted model | Validated smoke |
| Arch Linux x86_64 | Validated smoke | Same host-mounted model | Validated smoke under the existing `linux-lts` boundary |

KITPro does not mount or credential network shares. The operating system must
mount them first. Missing or changed mount identity fails closed.

Minimum recommendation: 2 CPU cores, 4 GiB RAM, and 20 GiB free system disk,
plus application-data capacity. KITPro listens locally by default; application
services are internal until an administrator enables loopback or exact-address
LAN exposure.
