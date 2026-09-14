# Process-kill SQLite crash matrix — 2026-09-13

Using `modernc.org/sqlite`, WAL, foreign keys, and `busy_timeout=250ms`, a
dedicated parent/child Go harness performed 10 repetitions each of committed
and uncommitted transaction termination.

Result: **20/20 PASS**. Committed rows survived SIGKILL; uncommitted rows were
absent after reopen; every integrity check returned `ok`.
