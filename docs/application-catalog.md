# Application catalog

KITPro ships a small trusted catalog. Every entry is strict schema-versioned
JSON, resolves to an immutable image digest, and passes the same helper-side
runtime validation. Catalog content cannot request host paths, Docker socket
access, privileged mode, host namespaces, devices, capabilities, or host port
bindings. Service exposure is an installation policy and defaults to internal.

## Ollama

- Category: AI
- Trusted release: 0.34.0
- Upstream: [Ollama](https://github.com/ollama/ollama)
- Model: one official `ollama/ollama` container pinned by digest
- Storage: persistent models at `/root/.ollama`
- Service: HTTP API on port 11434, private by default
- Acceleration: optional NVIDIA with explicit CPU fallback
- Limits: no automatic model downloads; AMD ROCm and Intel acceleration are not enabled for this release; NVIDIA is validated only within the published hardware support matrix

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
| Open WebUI | 0.11.3 | `ghcr.io/open-webui/open-webui@sha256:9cd136effce6bb12a6a1988a35ab3b82cb40c48a6768fceeb17c83baf7cfac9c` | `data` at `/app/backend/data` | HTTP 8080 | Open WebUI license |
| IT-Tools | 2024.10.22-7ca5933 | `docker.io/corentinth/it-tools@sha256:6f177c156b9466610e0f2093e24668b78da501c66f0054f98bccb582b74ab26b` | None | HTTP 80 | GPL-3.0 |
| Ollama | 0.34.0 | `docker.io/ollama/ollama@sha256:aa6f86f01fee264c81f1edd9083ebfb07c8116d95d8bedd1ad470874b66a40b4` | `models` at `/root/.ollama` | HTTP 11434 | MIT |

The ten single-container releases are pinned to their `linux/amd64` platform digest. Mutable
tags and release names are display and provenance metadata, not deployment
identity. Persistent host paths are derived as
`/srv/kitpro/apps/<application>/<installation>/<storage>/`.

FreshRSS, Uptime Kuma, and Memos use their browser setup flows for initial
credentials. Mealie ships with typed non-secret defaults `ALLOW_SIGNUP=false`
and `TZ=UTC`; its first database migration can take several minutes. No entry
contains a credential or accepts arbitrary environment variables.

Open WebUI starts with authentication enabled and keeps users, settings, and
chats in its persistent data store. KITPro generates and preserves its
`WEBUI_SECRET_KEY`; the value is never displayed. The application installs
without an LLM. An administrator configures a remote or OpenAI-compatible
backend in Open WebUI after installation. KITPro does not bundle Ollama, API
keys, host networking, or device access. Containers currently use Docker's
ordinary bridge egress, so configured external APIs are reachable; KITPro does
not claim an egress firewall or direct access to services bound only on host
loopback.

IT-Tools is intentionally stateless. It needs one HTTP container, no storage,
secrets, devices, capabilities, host networking, or background components.

Open WebUI and Ollama remain independent installations. KITPro installation networks are isolated, and no trusted cross-installation service-discovery primitive exists. Administrators can configure a separately reachable Ollama endpoint in Open WebUI, but KITPro does not add host networking or inject an unvalidated URL.

## GPU candidate decisions

| Candidate | Decision | Reason |
|---|---|---|
| LocalAI | REJECT FOR CURRENT MODEL | The pinned official server image fetches an unsigned backend from a mutable `latest` OCI tag during model installation, breaking end-to-end immutable provenance. |
| Open WebUI and Ollama pairing | REJECT FOR CURRENT MODEL | Safe cross-installation service discovery is not yet available; independent applications remain supported. |
| ComfyUI | REJECT FOR CURRENT MODEL | Upstream does not publish a stable official production container suitable for an immutable trusted release. |
| Jellyfin | REJECT FOR CURRENT MODEL | A useful deployment requires media-library mounts; trusted external storage/import is not implemented. |
| Frigate | REJECT FOR CURRENT MODEL | Camera configuration, shared-memory sizing, and Coral/USB/PCI device needs exceed current bounded classes. |
| whisper.cpp server | REJECT FOR CURRENT MODEL | Official images track branches/commits and require a model bootstrap/selection contract KITPro does not yet provide. |
| InvokeAI | REJECT FOR CURRENT MODEL | Official GPU container tags track main/commit builds rather than a stable release identity suitable for the trusted catalog. |

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

Application Catalog Expansion V3 also evaluated n8n 2.38.7, Flowise 3.1.4,
Stirling PDF 2.14.3, and Securo 0.15.1. n8n and Flowise run as an unprivileged
image user and cannot initialize KITPro's root-owned `0700` bind storage; the
helper deliberately retains an empty capability set instead of gaining
`CAP_CHOWN`. Stirling PDF's current first-run fallback creates a known default
administrator credential when explicit credentials are absent, while KITPro's
non-disclosing secret contract has no safe one-time credential-reveal flow.
Securo's supported production layout requires PostgreSQL, Redis, migrations,
web/backend services, and Celery worker/beat components with readiness-gated
startup and assembled shared connection secrets. Schema v2 supports components
and ordering but not those readiness/bootstrap contracts. None was collapsed
into an unsafe or unsupported topology.

## Upstream provenance

The following official sources were retrieved on 2026-09-13:

- FreshRSS: [project releases](https://github.com/FreshRSS/FreshRSS/releases), [official image](https://hub.docker.com/r/freshrss/freshrss)
- Uptime Kuma: [project and Docker instructions](https://github.com/louislam/uptime-kuma), [2.3.1 release](https://github.com/louislam/uptime-kuma/releases/tag/2.3.1), [image tag policy](https://github.com/louislam/uptime-kuma/wiki/Docker-Tags)
- Mealie: [project releases](https://github.com/mealie-recipes/mealie/releases), [official GHCR package](https://github.com/mealie-recipes/mealie/pkgs/container/mealie), [backend configuration](https://github.com/mealie-recipes/mealie/blob/mealie-next/docs/docs/documentation/getting-started/installation/backend-config.md)
- Memos: [project releases](https://github.com/usememos/memos/releases), [official Docker deployment](https://usememos.com/docs/deploy/docker), [official GHCR package](https://github.com/usememos/memos/pkgs/container/memos)
- Linkding: [1.46.2 release](https://github.com/sissbruecker/linkding/releases/tag/v1.46.2), [official deployment README](https://github.com/sissbruecker/linkding/blob/master/README.md), [configuration options](https://github.com/sissbruecker/linkding/blob/master/docs/src/content/docs/options.md)
- Open WebUI: [official Docker quick start](https://docs.openwebui.com/getting-started/quick-start/), [releases](https://github.com/open-webui/open-webui/releases)
- n8n: [official Docker installation](https://docs.n8n.io/hosting/installation/docker/), [releases](https://github.com/n8n-io/n8n/releases)
- Flowise: [official environment reference](https://docs.flowiseai.com/configuration/environment-variables), [releases](https://github.com/FlowiseAI/Flowise/releases)
- Stirling PDF: [official Docker guide](https://docs.stirlingpdf.com/Installation/Docker%20Install/), [releases](https://github.com/Stirling-Tools/Stirling-PDF/releases)
- IT-Tools: [official repository and deployment instructions](https://github.com/CorentinTh/it-tools)
- Securo: [official repository and production topology](https://github.com/securo-finance/securo)
- LocalAI: [4.9.0 release](https://github.com/mudler/LocalAI/releases/tag/v4.9.0), [official container guide](https://localai.io/basics/container/), [authentication](https://localai.io/advanced/auth/), [model management](https://localai.io/models/)
- ComfyUI: [official documentation](https://docs.comfy.org/)
- Jellyfin: [official container installation](https://jellyfin.org/docs/general/installation/container/)
- Frigate: [official installation](https://docs.frigate.video/frigate/installation/)
- whisper.cpp: [official repository and container definitions](https://github.com/ggml-org/whisper.cpp)
- InvokeAI: [official Docker documentation](https://github.com/invoke-ai/InvokeAI/blob/main/docker/README.md)

Paperless-ngx is the first schema-version-2 multi-container entry. It uses a
Paperless web component and an internal Redis broker on one
installation-owned bridge; only the web service is user-exposable. Debian 13,
Ubuntu 26.04, and Arch Linux live acceptance is recorded in the multi-container
architecture result.

Adding or updating an entry requires repeating digest resolution, strict
manifest tests, helper-plan validation, persistence/recreation tests, exposure
tests, reconciliation, and supported-host integration. Application upgrades
remain a separate future operation.
