# FreshRSS storage reuse follow-up

Date: 2026-09-13

This follow-up closes the FreshRSS-specific reinstall failure recorded in the
earlier FreshRSS acceptance records. The generic lifecycle now gives each
installation a stable identity and derives persistent storage from that
identity rather than from a disposable runtime/container generation.

The authenticated production flow removed FreshRSS runtime while preserving
its marker and then recreated the same installation. The recreate operation
advanced the runtime generation to 2, reused the original storage path, kept
the marker checksum unchanged, started FreshRSS successfully, and returned
`exact` reconciliation. No FreshRSS-specific helper or Docker exception was
introduced. Generic platform restart/reboot certifications remain referenced
from prior immutable records and were intentionally not repeated here.

`FIRST REAL APPLICATION GATE: PASS`
