# Application catalog

KITPro ships a small trusted catalog. Every entry is strict schema-versioned
JSON, resolves to an immutable image digest, and passes the same helper-side
runtime validation. Catalog content cannot request host paths, Docker socket
access, privileged mode, host namespaces, devices, capabilities, or host port
bindings. Service exposure is an installation policy and defaults to internal.

The 15 visible entries use manifest schema version 7. Their category,
application kind, catalog status, official links, and non-derivable limitations
come from the manifest. The API and server-rendered catalog use that same
source. Logo keys can reference reviewed packaged assets; entries without one
keep the generated-initials fallback. The internal BusyBox lifecycle fixture
remains on schema version 6 to preserve backward-compatibility coverage.

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

| Application | Category | Purpose | Model | Storage model | GPU | Exposure and major limitation |
| --- | --- | --- | --- | --- | --- | --- |
| FreshRSS | Reading | RSS reader | Single | Managed data/extensions | CPU | HTTP, private by default; browser bootstrap |
| Uptime Kuma | Monitoring | Service monitoring | Single | Managed data | CPU | HTTP, private by default |
| Mealie | Food and recipes | Recipe management | Single | Managed data | CPU | HTTP, private by default; signup disabled by default |
| Memos | Notes | Notes and captures | Single | Managed data | CPU | HTTP, private by default |
| Actual Budget | Finance | Local budgeting | Single | Managed data | CPU | HTTP, private by default |
| Vaultwarden | Security | Password vault | Single | Managed data | CPU | HTTP, private by default; public TLS is outside KITPro |
| Home Assistant | Home automation | Home dashboard | Single | Managed config | CPU | HTTP, private by default; no arbitrary devices or host network |
| Paperless-ngx | Documents | Document archive | Multi | Managed app, media, consume, export, and broker data | CPU | Web only; Redis remains internal |
| Open WebUI | AI | Authenticated AI interface | Single | Managed data and generated secret | CPU | HTTP, private by default; backend configured separately |
| IT-Tools | Developer Tools | Browser utilities | Single | Stateless | CPU | HTTP, private by default |
| Ollama | AI | Local model runtime | Single | Managed models | CPU or optional certified NVIDIA | API, private by default; no automatic model downloads |
| Jellyfin | Media | Video/music library | Single | Managed config/cache plus read-only trusted media | CPU; NVIDIA optional but transcoding unvalidated | HTTP, private by default; one media root |
| Navidrome | Music | Music streaming | Single | Managed database plus read-only trusted music | CPU | HTTP, private by default |
| Audiobookshelf | Media | Audiobook streaming | Single | Managed config/metadata plus read-only trusted library | CPU | HTTP, private by default |
| SFTPGo | Files | Scoped file access | Single | Managed config plus exclusive trusted read-write root | CPU | Web UI exposable; SFTP remains internal |

All user-facing services start internal-only. An administrator may select
loopback or one configured LAN address; wildcard publication, host networking,
and automatic Internet exposure are unavailable.

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
| Jellyfin | 12.1 | `docker.io/jellyfin/jellyfin@sha256:326be1010b16c92e492f6c7dd6fd105943db84ce723c73183279a1ab357b8f9b` | managed `/config` and `/cache`; trusted read-only `/media` | HTTP 8096 | GPL-2.0-or-later |
| Navidrome | 0.64.0 | `docker.io/deluan/navidrome@sha256:1a64cbb2603cec5d2615c3a27e91442436b2229583408a55cdc8d85705b95e65` | managed `/data`; trusted read-only `/music` | HTTP 4533 | GPL-3.0 |
| Audiobookshelf | 2.36.0 | `ghcr.io/advplyr/audiobookshelf@sha256:e388e90e381ae3fa8660346612b2955f2c555ede81c9c286e2218bdf966b4de8` | managed `/config` and `/metadata`; trusted read-only `/audiobooks` | HTTP 80 | GPL-3.0 |
| SFTPGo | 2.7.5 | `ghcr.io/drakkan/sftpgo@sha256:d819bcea946470940416b63604f820aee965a02127b07126785e279fa311258e` | managed config; exclusive trusted read-write `/srv/sftpgo/data` | HTTP 8080; internal SFTP 2022/TCP | AGPL-3.0-only |

The fourteen single-container releases are pinned to their `linux/amd64` platform digest. Mutable
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
| Jellyfin | ACCEPT | Schema v4 binds one administrator-approved media root read-only while config and cache remain KITPro-managed. |
| Frigate | REJECT FOR CURRENT MODEL | Camera configuration, shared-memory sizing, and Coral/USB/PCI device needs exceed current bounded classes. |
| whisper.cpp server | REJECT FOR CURRENT MODEL | Official images track branches/commits and require a model bootstrap/selection contract KITPro does not yet provide. |
| InvokeAI | REJECT FOR CURRENT MODEL | Official GPU container tags track main/commit builds rather than a stable release identity suitable for the trusted catalog. |

## Candidate decision

For the media and data-heavy expansion, Navidrome and Audiobookshelf are
accepted as non-root, read-only media consumers. SFTPGo is accepted as the
first imported read-write consumer; its first-run administrator setup avoids a
known default password, and its imported root is exclusive while writable.

Immich 3.2.0 is rejected for the current model. Its official deployment has
Immich Server, PostgreSQL with VectorChord, Valkey, and machine learning. A
faithful release needs shared component secrets, health-gated dependencies,
bounded PostgreSQL shared memory, atomic multi-component updates, and
database-aware backup/rollback. KITPro does not substitute SQLite or omit ML.

The original File Browser 2.63.23 is rejected because upstream archived the
repository and ended fixes, including security fixes. Syncthing remains
rejected: its official container needs 22000/TCP+UDP and 21027/UDP, and
upstream documents that bridge networking prevents correct local address
discovery. Typed UDP alone would not make the topology correct without host
networking, which KITPro prohibits.

For the storage and media milestone, Jellyfin is **accepted** with one required
read-only trusted media root. Syncthing 2.1.5 is **rejected for the current
model**: its authoritative container exposes the GUI on 8384/TCP, synchronization
on 22000/TCP and UDP, and discovery on 21027/UDP, while upstream strongly
recommends host networking for correct LAN discovery. KITPro has neither host
networking nor typed UDP multi-service exposure, and does not ship an incomplete
GUI-only topology. Immich is **rejected for the current model**: its supported
production deployment is a Compose stack with server, PostgreSQL, Redis, and
machine-learning components; its upload library needs read-write lifecycle
semantics and PostgreSQL must remain on a compatible local filesystem. The
current milestone does not distort that topology into a simpler unsupported
deployment.

Older candidate decisions are preserved in dated evidence reports. They are not
current capability statements: generated secrets and bounded managed-storage
ownership now exist. Deferred applications must be researched again against the
current schema rather than accepted from an old rejection or an old workaround.

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
- LocalAI: [4.9.0 release](https://github.com/mudler/LocalAI/releases/tag/v4.9.0), [official container guide](https://localai.io/basics/container/), [authentication](https://localai.io/docs/features/authentication/), [model management](https://localai.io/models/)
- ComfyUI: [official documentation](https://docs.comfy.org/)
- Jellyfin: [official container installation](https://jellyfin.org/docs/general/installation/container/)
- Syncthing: [official container guide](https://github.com/syncthing/syncthing/blob/main/README-Docker.md), [official Dockerfile and ports](https://github.com/syncthing/syncthing/blob/main/Dockerfile)
- Immich: [official Docker Compose installation](https://docs.immich.app/install/docker-compose/), [production requirements](https://docs.immich.app/install/requirements/)
- Frigate: [official installation](https://docs.frigate.video/frigate/installation/)
- whisper.cpp: [official repository and container definitions](https://github.com/ggml-org/whisper.cpp)
- InvokeAI: [official Docker documentation](https://github.com/invoke-ai/InvokeAI/blob/main/docker/README.md)

Paperless-ngx is the first schema-version-2 multi-container entry. It uses a
Paperless web component and an internal Redis broker on one
installation-owned bridge; only the web service is user-exposable. Debian 13,
Ubuntu 26.04, and Arch Linux live acceptance is recorded in the multi-container
architecture result.

Adding or updating an entry requires digest resolution, strict manifest tests,
helper-plan validation, persistence/recreation tests, exposure tests,
reconciliation, and supported-host integration. Trusted administrator-initiated
application updates are supported only where the catalog defines a reviewed
release transition.
