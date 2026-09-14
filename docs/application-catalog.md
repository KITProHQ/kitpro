# Application catalog

KITPro ships a small trusted catalog. Every entry is strict schema-versioned
JSON, resolves to an immutable image digest, and passes the same helper-side
runtime validation. Catalog content cannot request host paths, Docker socket
access, privileged mode, host namespaces, devices, capabilities, or host port
bindings. Service exposure is an installation policy and defaults to internal.

## Supported applications

| Application | Release | Image identity | Persistent storage | Service | License |
| --- | --- | --- | --- | --- | --- |
| FreshRSS | 1.29.1 | `docker.io/freshrss/freshrss@sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21` | `data` at `/var/www/FreshRSS/data`; `extensions` at `/var/www/FreshRSS/extensions` | HTTP 80 | AGPL-3.0 |
| Uptime Kuma | 2.3.1 | `docker.io/louislam/uptime-kuma@sha256:92fd01c488771d1bcb0b299770255c06994ab7e4f079b7c7fcf52b8e08789a67` | `data` at `/app/data` | HTTP 3001 | MIT |
| Mealie | 3.24.0 | `ghcr.io/mealie-recipes/mealie@sha256:3d2384661634e954c12ec27bb5b25a0263832f9e39044f145d726d388e9f8268` | `data` at `/app/data` | HTTP 9000 | AGPL-3.0 |
| Memos | 0.30.0 | `docker.io/neosmemo/memos@sha256:51a4cef418b1f173ac37139ad99de08da5b8662136007231d3ac8a0498a3095a` | `data` at `/var/opt/memos` | HTTP 5230 | MIT |
| Actual Budget | 26.9.0 | `docker.io/actualbudget/actual-server@sha256:06080cca505895fffd5736089920001e979bf9595f757d1d5b9ebfc99722c410` | `data` at `/data` | HTTP 5006 | MIT |
| Vaultwarden | 1.37.2 | `docker.io/vaultwarden/server@sha256:5d326778c22f063d093d6b0c9c766a28249561632266776f2c93132ab0ad3a80` | `data` at `/data` | HTTP 80 | AGPL-3.0 |
| Home Assistant | stable | `ghcr.io/home-assistant/home-assistant@sha256:542890f4a7ef9269b7a5ac23ada303b327537c62fa0f866e49daebc61cb44caa` | `config` at `/config` | HTTP 8123 | Apache-2.0 |
| Paperless-ngx | 2.20.15 + Redis 7.4.11 | `docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f` plus `docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7` | web `data`, `media`, `consume`, `export`; broker `data` | HTTP 8000 (web only) | GPL-3.0 |

The seven single-container releases are pinned to their `linux/amd64` platform digest. Mutable
tags and release names are display and provenance metadata, not deployment
identity. Persistent host paths are derived as
`/srv/kitpro/apps/<application>/<installation>/<storage>/`.

FreshRSS, Uptime Kuma, and Memos use their browser setup flows for initial
credentials. Mealie ships with typed non-secret defaults `ALLOW_SIGNUP=false`
and `TZ=UTC`; its first database migration can take several minutes. No entry
contains a credential or accepts arbitrary environment variables.

## Candidate decision

Linkding 1.46.2 is **rejected for the current model**. Its supported initial
administrator paths require either an interactive `createsuperuser` command or
the `LD_SUPERUSER_NAME` and `LD_SUPERUSER_PASSWORD` environment variables.
KITPro does not yet have a production secret store or a typed one-time bootstrap
operation, and weakening the manifest boundary to inject a password would be
unsafe. Memos supplies comparable self-hosted notes and link-capture value while
fitting the existing browser-setup, single-container model.

Gitea was also evaluated as a possible substitute. Its rootful image could not
traverse KITPro's root-owned `0750` installation storage without an ownership
policy that the current manifest model does not express. No application-specific
`chown` or privileged exception was added.

## Upstream provenance

The following official sources were retrieved on 2026-09-13:

- FreshRSS: [project releases](https://github.com/FreshRSS/FreshRSS/releases), [official image](https://hub.docker.com/r/freshrss/freshrss)
- Uptime Kuma: [project and Docker instructions](https://github.com/louislam/uptime-kuma), [2.3.1 release](https://github.com/louislam/uptime-kuma/releases/tag/2.3.1), [image tag policy](https://github.com/louislam/uptime-kuma/wiki/Docker-Tags)
- Mealie: [project releases](https://github.com/mealie-recipes/mealie/releases), [official GHCR package](https://github.com/mealie-recipes/mealie/pkgs/container/mealie), [backend configuration](https://github.com/mealie-recipes/mealie/blob/mealie-next/docs/docs/documentation/getting-started/installation/backend-config.md)
- Memos: [project releases](https://github.com/usememos/memos/releases), [official Docker deployment](https://usememos.com/docs/deploy/docker), [official GHCR package](https://github.com/usememos/memos/pkgs/container/memos)
- Linkding: [1.46.2 release](https://github.com/sissbruecker/linkding/releases/tag/v1.46.2), [official deployment README](https://github.com/sissbruecker/linkding/blob/master/README.md), [configuration options](https://github.com/sissbruecker/linkding/blob/master/docs/src/content/docs/options.md)

Paperless-ngx is the first schema-version-2 multi-container entry. It uses a
Paperless web component and an internal Redis broker on one
installation-owned bridge; only the web service is user-exposable. Debian 13,
Ubuntu 26.04, and Arch Linux live acceptance is recorded in the multi-container
architecture result.

Adding or updating an entry requires repeating digest resolution, strict
manifest tests, helper-plan validation, persistence/recreation tests, exposure
tests, reconciliation, and supported-host integration. Application upgrades
remain a separate future operation.
