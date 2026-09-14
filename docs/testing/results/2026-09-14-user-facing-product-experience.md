# User-facing product experience

## Review scope

This pass covers the public-alpha dashboard, catalog, setup/login, installed
application controls, service access language, responsive layout, and release
onboarding documentation. Core API, helper, storage, exposure, and update
security contracts were not redesigned.

## Measured result

- Setup explains local administration, server-owned data, and next steps;
  password fields have labels and the existing 12-character policy.
- Dashboard now leads with server readiness, catalog choices, installed app
  state, activity, and plain-language access modes. Internal IDs and Docker
  details are no longer primary information.
- Catalog cards show purpose, trusted release, and a clear Install action.
- Installed apps expose Start, Stop, Recreate, service access, and update
  controls; Paperless-ngx remains one logical application.
- Access labels are Private, This server only, and Local network. Open App is
  shown only when a real endpoint exists.
- Inline CSS uses semantic headings, labels, focus-visible controls, responsive
  cards, and a 600px narrow-screen layout. Keyboard and text-only review found
  no color-only status dependency or overflowing primary controls.
- Public Alpha status, support matrix, quickstart, security policy, recovery
  limits, and issue-reporting guidance are linked from public documentation.

The existing Debian full acceptance and Ubuntu/Arch smoke evidence remains the
platform basis; this UX pass changes only embedded presentation assets. The
candidate artifacts were rebuilt reproducibly after the asset change:

- Debian `.deb`: `23e078c81e43e55d7779caa19ef785edb786f52deb57f5b6efc02cc82895be71`
- Arch `.pkg.tar.zst`: `793a9ec514196f6ccf93fb055eed4b337e612c2ba3cf0f5f5612b160d806f4ed`
- SBOM: `0f0108551c69d6d173da4169b44270a28c6ec252b933f2fc1e3b51eb1cd3edb4`

Known limitations are documented in `docs/release/known-limitations.md`.
