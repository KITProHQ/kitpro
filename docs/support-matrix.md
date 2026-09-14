# Platform support matrix

| Platform | Status | Boundary |
| --- | --- | --- |
| Debian 13 amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Ubuntu 26.04 LTS amd64 | Supported | Rootful Docker, enforcing AppArmor |
| Arch Linux x86_64 | Supported | `linux-lts`, fully updated official repositories, rootful Docker, enforcing AppArmor; no partial upgrades |
| Rocky Linux 10 amd64 | Experimental | Validate SELinux and Docker integration before use |

Minimum recommendation: 2 CPU cores, 4 GiB RAM, and 20 GiB free system disk,
plus application-data capacity. KITPro listens locally by default; application
services are internal until an administrator enables loopback or exact-address
LAN exposure.
