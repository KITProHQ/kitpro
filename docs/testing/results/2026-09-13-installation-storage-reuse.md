# Installation/storage identity validation

Date: 2026-09-13

## Result

`INSTALLATION/STORAGE SEMANTICS GATE: PASS`

The production slice now distinguishes catalog application identity,
stable installation identity, and disposable runtime generation. New installs
create a fresh installation and generation 1. Runtime removal preserves the
installation record and `/srv/kitpro/apps/<application>/<installation>/`
storage. Recreate uses the same installation and storage while advancing the
runtime generation.

## Debian evidence

Using the authenticated production API on Debian VM 500, FreshRSS installation
`inst-870bce6bdc981d77` was created, stopped, and removed. The marker at
`/srv/kitpro/apps/freshrss/inst-870bce6bdc981d77/data/marker` retained SHA-256
`68d7ebe858e6a0319977eba51d3faf5ccb0f9d3e83657e9c2b6ff25d3669702d` after
runtime removal. Recreate returned a successful operation and used the same
installation with runtime generation 2 (`kitpro-freshrss-...-g2`), reusing the
existing storage. FreshRSS internal HTTP and exact reconciliation were
validated before disposable cleanup.

## Semantics

- `runtime_removed` is an intentional desired state, distinct from unexpected
  `missing` reconciliation.
- Container and network names include runtime generation; persistent paths do
  not.
- The helper remains the authority for Docker ownership and independently
  validates installation/runtime identity and approved storage mappings.
- A catalog install always creates a new installation; recreation is explicit.
- Removing runtime never deletes application data. A future explicit “delete
  application data” action is separate and destructive.

## Validation

Local production Go tests, vet, formatting, and `git diff --check` passed.
The Debian SSH verification also confirmed API/helper services remained active
and direct Docker access by the unprivileged API identity was denied.
