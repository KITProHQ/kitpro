# Documentation release pass

Date: 2026-09-14

## Workflow

- `document-generate`: completed with both inline entry-point updates and
  standalone Diátaxis-oriented reference/how-to documentation.
- `document-release`: completed as a factual cross-document synchronization
  against the shipped alpha implementation.

## Changes

- Added `docs/api-reference.md` covering the supported authenticated read,
  lifecycle, exposure, and trusted-update surfaces.
- Updated README, install guides, architecture/vision language, catalog
  references, application-manifest schema notes, and decision statuses.
- Corrected stale alpha package examples and replaced the obsolete catalog-v2
  inventory with a provenance pointer to the current catalog.
- Kept historical validation records intact; they remain evidence of the
  milestones they describe rather than current installation instructions.

## Validation

- Relative Markdown-link audit: PASS (0 broken links).
- JSON validation: PASS for release manifest and SBOM.
- Sensitive-data scan: PASS; no credentials, keys, tokens, VM secrets, or
  private Forgejo details were added to user-facing docs.
- `go test ./...`: PASS.
- `go vet ./...`: PASS.
- `gofmt` and `git diff --check`: PASS.

No release artifacts were rebuilt. Documentation changes do not modify
packaged assets, binary behavior, or release version metadata. Public GitHub
publication was not attempted. The private `v0.1.0-alpha.1` tag remains
untouched.

## Remaining documentation limitations

- The live `https://os.kitpro.us` reference could not be resolved from the
  validation environment, so exact brand typography and color claims remain
  intentionally conservative.
- A public GitHub URL is not documented because the public repository does not
  yet exist.
