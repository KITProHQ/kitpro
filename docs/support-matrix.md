# Platform support matrix

| Platform | Status | Boundary |
| --- | --- | --- |
| Debian 13 amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Ubuntu 26.04 LTS amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Arch Linux x86_64 | Supported | `linux-lts`, fully updated official repositories, rootful Docker, enforcing AppArmor; no partial upgrades |
| Rocky Linux 10 amd64 | Experimental, promotion-ready | Rootful Podman 5, Quadlet, crun, SELinux Enforcing, and firewalld; clean-host and application backup/restore acceptance passed, but no supported Rocky release is published yet |

## Capability matrix

| Capability | Debian 13 | Ubuntu 26.04 LTS | Arch Linux `linux-lts` | Rocky Linux 10 |
| --- | --- | --- | --- | --- |
| Basic KITPro | Certified | Certified | Certified | Acceptance passed; promotion pending |
| Mandatory access control | AppArmor required and certified | AppArmor required and certified | AppArmor required and certified | SELinux Enforcing acceptance passed |
| Container runtime | Rootful Docker certified | Rootful Docker certified | Rootful Docker certified | Rootful Podman 5 and Quadlet acceptance passed |
| 15-app trusted catalog | Certified | Certified | Certified | Acceptance passed |
| Multi-container Paperless-ngx | Certified | Certified | Certified | Acceptance passed, including backup/restore |
| Trusted external storage | Certified | Smoke certified | Smoke certified | Local read-only/read-write acceptance passed; NAS not exercised |
| Ollama CPU | Certified | Certified | Certified | Acceptance passed |
| NVIDIA Ollama inference | Certified: RTX A2000 / Toolkit 1.20.0 | Certified: RTX A2000 / Toolkit 1.20.0 | Certified: RTX A2000 / Toolkit 1.20.0 | Not certified |
| AMD accelerated workload | Not certified | Not certified | Device scoping evidence only; compute not certified | Not certified |
| Intel accelerated workload | Not certified | Not certified | Device scoping evidence only; workload not certified | Not certified |
| Existing host-mounted NFS/CIFS root | Detection implemented; KITPro does not mount shares | Detection implemented; KITPro does not mount shares | Detection implemented; KITPro does not mount shares | Not certified |
| Jellyfin, Navidrome, Audiobookshelf, SFTPGo | Certified | Smoke certified | Smoke certified | Acceptance passed |
| Jellyfin NVIDIA transcoding | Not certified | Not certified | Not certified | Not certified |

## Hardware acceleration

| Platform | Discovery | Ollama CPU | NVIDIA | AMD | Intel/VAAPI |
|---|---|---|---|---|---|
| Debian 13 amd64 | Validated | Validated | Validated: driver 550.163.01 and NVIDIA Container Toolkit 1.20.0 | Architecture only | Architecture only |
| Ubuntu 26.04 LTS amd64 | Validated | Validated | Validated: driver 580.178.04 and NVIDIA Container Toolkit 1.20.0 | Unvalidated | Unvalidated |
| Arch Linux x86_64 | Validated | Validated | Validated: `linux-lts`, open DKMS driver 615.71.09, and NVIDIA Container Toolkit 1.20.0 | Device scoping validated locally; compute unvalidated | Device scoping validated locally; accelerated workload unvalidated |
| Rocky Linux 10 amd64 | Validated | Validated | Out of scope pending CDI design and certification | Not certified | Not certified |

GPU support is not universal. It requires visible vendor hardware, its host driver, a suitable render/compute node, and vendor runtime integration where applicable. QXL virtual graphics does not qualify.
The validated NVIDIA device was an RTX A2000 12GB passed through exclusively to
one test VM at a time. AMD compute and Intel acceleration are not certified.

Ollama and Jellyfin declare optional NVIDIA access. Ollama inference is live-certified. Jellyfin transcoding requires separate application-level certification and is not claimed solely because its container can see the GPU.

## Trusted external storage

| Platform | Local trusted roots | Existing NFS/SMB mount registration | Jellyfin |
| --- | --- | --- | --- |
| Debian 13 amd64 | Validated | Filesystem detection implemented; disposable live NAS unavailable | Validated with read-only media, drift, recreation, and reboot |
| Ubuntu 26.04 LTS amd64 | Validated smoke | Same host-mounted model | Validated smoke |
| Arch Linux x86_64 | Validated smoke | Same host-mounted model | Validated smoke under the existing `linux-lts` boundary |
| Rocky Linux 10 amd64 | Validated | Host-mounted NAS unavailable during acceptance | Validated with SELinux Enforcing |

Navidrome and Audiobookshelf use read-only imported libraries. SFTPGo is the
first read-write consumer and requires an exclusive root. Navidrome and SFTPGo
passed live authenticated install, exact-mount, private-exposure, and reboot
smoke tests on Debian, Ubuntu, and Arch. Audiobookshelf received full Debian
acceptance with the same packaged schema and helper boundary.

KITPro does not mount or credential network shares. The operating system must
mount them first. Missing or changed mount identity fails closed.

Minimum recommendation: 2 CPU cores, 4 GiB RAM, and 20 GiB free system disk,
plus application-data capacity. KITPro listens locally by default; application
services are internal until an administrator enables loopback or exact-address
LAN exposure.
