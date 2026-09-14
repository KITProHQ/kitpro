# Debian persistence follow-up — 2026-09-13

**DEBIAN SQLITE/REBOOT GATE: FAIL**

## Tmpfiles lifecycle

Installed disposable rule:

```text
d /run/kitpro 0750 root kitpro-api-test -
```

`systemd-tmpfiles --create` produced `root:kitpro-api-test` mode `0750`.
After reboot, before any manual repair, the same ownership and mode were
present. This resolved the previous runtime-directory defect.

## Real Debian measurements

The compiled disposable Go persistence probe using `modernc.org/sqlite` reported:

```text
readers_writers=PASS elapsed_ms=2
backup=PASS generation=1 integrity=ok foreign_keys=
fencing=PASS
```

WAL and SHM files were created with root ownership and mode `0600`. The API
identity could write `control.db` but could not write, delete, rename, or modify
helper state. The helper identity could not write the control database.

## Reboot chain

Before reboot, a known helper receipt (`reboot-receipt`) was written. After
reboot:

- Docker: active
- `/run/kitpro`: automatically recreated as `root:kitpro-api-test` mode `0750`
- control database: preserved as `kitpro-api-test:kitpro-api-test` mode `0600`
- helper database: preserved as `root:root` mode `0600`
- AppArmor profile: loaded
- helper receipt replay: PASS
- helper Docker inspection: PASS

No manual `chown` or `chmod` was used before the post-reboot ownership check.

## Remaining blockers

The gate remains **FAIL** because the required full matrices were not all
executed on Debian:

- 10-reader/reader-writer/writer-writer contention and measured fencing under
  deliberate lock contention: NOT RUN as a dedicated matrix;
- process-kill committed/uncommitted/WAL crash cases: NOT RUN;
- migration failure matrix (future version, downgrade, preflight, backup hook):
  NOT RUN;
- active-WAL backup with concurrent reader and writer generations: NOT RUN as a
  dedicated run (the bounded `VACUUM INTO` probe passed generation and integrity
  checks).

The final busy-timeout recommendation remains `250ms` provisionally, based on
the successful bounded probe; a dedicated lock-hold measurement is still needed
before finalizing it.

## Cleanup

Disposable identities, units, AppArmor profile, tmpfiles rule, binaries,
databases, and validation storage were removed. Docker and snapshots were
preserved. No commits or pushes were made.
