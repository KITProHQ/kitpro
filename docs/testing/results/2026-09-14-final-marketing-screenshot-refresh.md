# Final marketing screenshot refresh

## Approved source

The public documentation screenshots were refreshed from the latest approved
captures in the existing KITPro screenshot automation artifact set. All seven
captures were made from the real polished KITPro Server interface. The root
README continues to use the dashboard, catalog, and installed-application
views; `software/server/README.md` continues to use the dashboard and installed
application views.

The website handoff adds the access, updates, mobile, and Paperless-ngx views.
Paperless-ngx is presented as one logical application.

## Sanitization

The final public images were visually inspected. They contain no credentials,
cookies, session material, test usernames, installation identifiers, image
digests, VM identifiers, private hostnames, or private endpoints. Technical
details remain collapsed.

## Validation

- README image paths resolve to tracked PNG files.
- All selected files are valid, non-empty PNG images.
- Markdown relative-link validation passes.
- `git diff --check` passes.
- The sensitive-data scan reports no release-blocking findings.
- Package artifacts were not rebuilt for documentation-only image changes.

## Public release state

The public GitHub repository already has an annotated `v0.1.0-alpha.1` tag and
prerelease. The tag predates this screenshot refresh and is not moved by this
work. A later release must use a new version if it is to identify the refreshed
clean-public source commit without rewriting tagged public history.
