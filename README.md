# KITPro Server — Public Alpha

KITPro Server is a local-first control panel for installing and operating a trusted set of self-hosted applications on your own Linux server. It exists to make Docker, persistence, private networking, updates, GPU access, and media storage understandable without giving a web application unrestricted control of the host.

The public alpha includes 15 reviewed applications: FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo. Images are pinned by digest; applications start private; persistent data survives runtime recreation.

Supported hosts are Debian 13 amd64, Ubuntu 26.04 LTS amd64, and fully updated Arch Linux x86_64 with `linux-lts`, rootful Docker, and enforcing AppArmor. Rocky Linux 10 remains experimental. NVIDIA acceleration is live-certified with an RTX A2000 12GB for Ollama; CPU fallback is supported. Administrator-approved local or host-mounted NFS/CIFS storage can be attached through typed read-only or exclusive read-write slots. KITPro does not accept arbitrary Docker configuration, devices, or host bind mounts.

> KITPro Server is alpha software. Read the [support matrix](docs/support-matrix.md) and [known limitations](docs/release/known-limitations.md) before relying on it for important data.

![KITPro Server dashboard](docs/assets/screenshots/kitpro-server-dashboard.png)

## Get started

1. Download a package from the [v0.1.0-alpha.11 prerelease](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11).
2. Follow the [public alpha quickstart](docs/release/quickstart.md) for Debian, Ubuntu, or Arch.
3. Open `http://127.0.0.1:8080/`, create the first local administrator, choose an app, and select its access mode.

Release notes and checksums attached to the prerelease are authoritative for downloaded packages.

## Explore

- [Current product brief](docs/product/kitpro-server-current-state.md)
- [Application catalog](docs/application-catalog.md)
- [Architecture](docs/architecture.md)
- [Security model](docs/security/current-security-boundary.md)
- [Trusted storage guide](docs/trusted-storage.md)
- [Hardware acceleration guide](docs/hardware-acceleration.md)
- [Screenshots and video visual inventory](docs/product/video-broll-inventory.md)
- [KITPro Server product page](https://kitpro.us/server)

The technical implementation entry point is [software/server/README.md](software/server/README.md). KITPro is licensed under the [Apache License 2.0](LICENSE). Future KITPro OS, hardware, and cloud ideas are separate from this Server alpha and are not current features.
