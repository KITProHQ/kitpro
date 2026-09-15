# KITPro screenshot integration — 2026-09-15

## Scope

Approved real KITPro Server captures from the demo automation project were
copied into stable public asset paths. No KITPro Server behavior or package
artifacts changed.

## Selected assets

The product page uses Dashboard, Catalog, Installed Application, Access, Updates,
and Mobile captures from the approved Debian validation set. The root README
uses Dashboard, Catalog, and Installed Application. `software/server/README.md`
uses Dashboard and Installed Application. Images are stored under
`docs/assets/screenshots/` and the site serves corresponding files from
`public/images/server/`.

The captures are real UI screenshots. Technical identifiers and loopback
endpoints are masked in the public copies; no credentials, cookies, or session
data are present. Paperless-ngx is represented by its real catalog card because
the validation host did not have a completed Paperless runtime.

## Validation

- Site `npm ci`: PASS (existing dependency audit reports 13 vulnerabilities; no dependency upgrades were made).
- Site lint: PASS with the repository's existing five `<img>` optimization warnings.
- Site production build: PASS.
- Local preview: `/` and `/server` HTTP 200; screenshot assets HTTP 200.
- Live deployment: source/config and public assets synced to the established site target; only `kitpro-os-site` was rebuilt/restarted.
- Live checks: homepage and `/server` HTTP 200; title, canonical URL, Open Graph metadata, and all six screenshot assets verified.
- Browser smoke: desktop, tablet, and phone widths rendered without horizontal overflow or console/runtime errors; all screenshot images decoded successfully.
- KITPro docs: relative image links and `git diff --check` pass; no sensitive-data findings in added content.

## Handoff

Use Dashboard, Catalog, and Installed Application for the first public product
overview. Access, Updates, and Mobile are supporting documentation or secondary
marketing material. No public GitHub release or tag was created.
