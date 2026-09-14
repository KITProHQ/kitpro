# Application catalog expansion V2

- Date: 2026-09-14
- Status: PASS; catalog expansion checkpoint carried into multi-container validation
- Baseline: `400097fd643787d4f5f9f7330acd5ca87ffa42d6`

## Candidate decisions

| Candidate | Decision | Reason |
| --- | --- | --- |
| Actual Budget | ACCEPT | Official Docker image, one server container, `/data`, HTTP 5006, no embedded secret required for initial launch. |
| Vaultwarden | ACCEPT | Official Docker image, one container, `/data`, HTTP 80; admin-token bootstrap remains optional and is not embedded. |
| Forgejo | REJECT FOR CURRENT MODEL | Official image is published through Codeberg, outside the accepted Docker Hub/GHCR registry scope; its SSH service also needs a separately reviewed exposure policy. |
| Home Assistant | ACCEPT as replacement | Official GHCR image, one container, `/config`, HTTP 8123, no privileged or host namespace requirement. |

## Immutable image inputs

The accepted manifests pin linux/amd64 child digests:

- Actual Budget 26.9.0, `docker.io/actualbudget/actual-server`, digest recorded in `actual-budget.json`.
- Vaultwarden 1.37.2, `docker.io/vaultwarden/server`, digest recorded in `vaultwarden.json`.
- Home Assistant stable, `ghcr.io/home-assistant/home-assistant`, digest recorded in `home-assistant.json`.

Sources were checked 2026-09-14 using [Actual Budget Docker guidance](https://actualbudget.org/docs/install/docker/), [Vaultwarden container documentation](https://github.com/dani-garcia/vaultwarden/blob/main/docker/README.md), [Home Assistant container guidance](https://www.home-assistant.io/installation/alternative/), and [Forgejo release guidance](https://forgejo.org/download/).

## Local validation

Strict catalog loading, digest/platform/storage/service validation, schema-v2
component parsing, dependency validation, and deterministic dependency ordering
pass in the Go test suite. Existing single-container catalog tests remain green.

The seven-app catalog remains schema-valid and unchanged by the multi-container
runtime work. Paperless-ngx is the first schema-v2 application; its live
platform evidence is recorded in the companion multi-container result. Existing
schema-v1 applications retain the original path.

The reproducible native Arch build used for this implementation checkpoint is
`software/server/dist/kitpro-server-0.1.0_alpha5-1-x86_64.pkg.tar.zst` with
SHA-256 `bf6c82caaab231f547dd17789a3ac7a902f3f00dd975e9a269ac7cef42e2be85`.
