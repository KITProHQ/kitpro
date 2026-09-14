# First production vertical slice pass attempt — 2026-09-13

Result: **FAIL**

Measured:

- ownership conflict: NOT RUN (no safe production API to alter labels without arbitrary Docker mutation);
- security drift: PASS — a stopped/running foreign network member was detected as `security_drift`, then exact state returned after removal;
- control/helper `VACUUM INTO` backups: PASS; restored copies returned `integrity_check=ok`, no foreign-key violations, and retained operation/receipt records;
- uninstall: PASS for service stop/disable, binary/unit/AppArmor removal while Docker and `/srv/kitpro` data remained;
- fresh redeployment: PASS after correcting a staging filename collision; AppArmor loaded, services active, dashboard and Docker status worked;
- persistent marker remained through uninstall/redeployment;
- fresh API workload create: PASS.

Remaining blockers are ownership-conflict execution and a full fresh stop/remove/reconciliation lifecycle after redeployment. No unrelated features were added.
