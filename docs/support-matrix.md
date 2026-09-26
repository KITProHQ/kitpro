# Platform support matrix

| Platform | Status | Boundary |
| --- | --- | --- |
| Debian 13 amd64 | Supported | Rootful Docker with an operator-selected non-overlapping address pool, enforcing AppArmor |
| Ubuntu 26.04 LTS amd64 | Supported | `.deb`, rootful Docker with an operator-selected non-overlapping address pool, enforcing AppArmor; must pass the supported-host release checks |
| Arch Linux x86_64 | Supported | `linux-lts`, fully updated official repositories, rootful Docker with an operator-selected non-overlapping address pool, enforcing AppArmor; no partial upgrades |
| Rocky Linux 10 amd64 | Experimental | Native RPM, rootful Podman 5, Quadlet, crun, SELinux Enforcing, and firewalld; alpha.13 application lifecycle is unavailable because the staged-generation contract is not implemented by the Podman adapter |

Podman is Experimental in alpha.13 and is used only by the experimental Rocky
Linux path. It is not a supported runtime on Debian, Arch Linux, or Ubuntu.

Every Supported Docker host must satisfy the
[Docker address-pool prerequisite](docker-address-pool-prerequisite.md). KITPro
does not modify Docker daemon configuration. Rocky Linux uses the separate
Experimental Podman/Quadlet path and is not covered by this Docker prerequisite.

## Capability matrix

| Capability | Debian 13 | Ubuntu 26.04 LTS | Arch Linux `linux-lts` | Rocky Linux 10 |
| --- | --- | --- | --- | --- |
| Basic KITPro | Qualified | Qualified | Qualified | Package and host qualified; application install unavailable in alpha.13 |
| Mandatory access control | AppArmor required and qualified | AppArmor required and qualified | AppArmor required and qualified | SELinux Enforcing package and host checks qualified |
| Container runtime | Rootful Docker qualified | Rootful Docker qualified | Rootful Docker qualified | Rootful Podman 5 and Quadlet package integration only; staged application lifecycle unavailable |
| 20-app trusted catalog | Qualified | Qualified | Qualified | Catalog is visible, but application deployment is unavailable |
| Multi-container Paperless-ngx | Qualified | Qualified | Qualified | Not runnable through the alpha.13 Podman lifecycle path |
| Trusted external storage | Qualified | Qualified | Qualified | Local read-only and read-write root registration passed; application attachment is blocked by the lifecycle limitation |
| Ollama CPU | Qualified | Qualified | Qualified | Not exercised because application deployment is unavailable |
| NVIDIA Ollama inference | Certified: RTX A2000 / Toolkit 1.20.0 | Development evidence: RTX A2000 / Toolkit 1.20.0 | Certified: RTX A2000 / Toolkit 1.20.0 | Not certified |
| AMD accelerated workload | Not certified | Not certified | Device scoping evidence only; compute not certified | Not certified |
| Intel accelerated workload | Not certified | Not certified | Device scoping evidence only; workload not certified | Not certified |
| Existing host-mounted NFS/CIFS root | Detection implemented; KITPro does not mount shares | Detection implemented; KITPro does not mount shares | Detection implemented; KITPro does not mount shares | Not certified |
| Jellyfin, Navidrome, Audiobookshelf, SFTPGo | Qualified | Qualified | Qualified | Not runnable through the alpha.13 Podman lifecycle path |
| Forgejo and Plex | Qualified | Qualified | Qualified | Not runnable through the alpha.13 Podman lifecycle path |
| Nextcloud, Pi-hole, and Syncthing Experimental profiles | Qualified within their documented constraints | Qualified within their documented constraints | Qualified within their documented constraints | Not runnable through the alpha.13 Podman lifecycle path |
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
| Rocky Linux 10 amd64 | Root registration validated | Host-mounted NAS unavailable during acceptance | Application attachment unavailable in alpha.13 |

Navidrome and Audiobookshelf use read-only imported libraries. SFTPGo is the
first read-write consumer and requires an exclusive root. The alpha.13
Supported-host qualification covers external-storage persistence and lifecycle
on Debian, Ubuntu, and Arch. Rocky root registration passed, but the
Experimental Podman lifecycle limitation prevents application attachment.

KITPro does not mount or credential network shares. The operating system must
mount them first. Missing or changed mount identity fails closed.

Minimum recommendation: 2 CPU cores, 4 GiB RAM, and 20 GiB free system disk,
plus application-data capacity. KITPro listens locally by default; application
services are internal until an administrator enables loopback or exact-address
LAN exposure.
