# Debian first production vertical slice follow-up — 2026-09-13

Result: **FAIL**

Persistent storage directory creation and marker preservation were verified for one removed workload. API and helper service restarts passed, and Docker restarted cleanly with services returning. However, after correcting Docker response handling, a fresh workload request returned `400 Bad Request` from Docker during creation; earlier API responses had incorrectly treated helper error responses as success. Consequently a valid managed workload could not be re-established for reboot/reconciliation acceptance.

Not completed: foreign-resource rejection, exact/missing/conflict reconciliation with a valid current resource, VM reboot with managed workload, backup sanity, and clean redeployment acceptance. The concrete implementation blocker is Docker create request compatibility in the adapter (the API now correctly surfaces helper failures).
