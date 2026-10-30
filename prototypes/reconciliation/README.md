# Durable reconciliation test fixture

> Disposable experiment only. This is not production KITPro code. Python and JSON files make the state transitions easy to inspect; they do not select the production language or database.

This fixture tests the state model in [ADR-0016](../../docs/decisions/0016-durable-state-and-reconciliation.md) against an in-memory Docker adapter and durable local files.

It models:

- separate control-plane desired state and helper-owned privileged state;
- atomic operation acceptance, prepared ownership intent, and durable per-instance leases;
- request hashes and exact replay;
- a dispatch marker written before the Docker call;
- unknown external outcomes resolved through deterministic resource inspection;
- full ownership checks that use helper state, object ID, deterministic name, labels, image identity, and network policy;
- stale receipt recovery after helper restart;
- state-loss behavior that never reconstructs ownership from labels; and
- administrator supersession as a new audit event.

The fixture implements one constrained `InstallTestInstance` operation. The ADR models full install, update, and uninstall workflows. This small operation is enough to test the shared receipt, ownership, crash, drift, and locking rules without starting product development.

## Run the tests

From this directory, run:

```sh
python3 -m unittest discover -s tests -v
```

The tests use only the Python standard library and temporary directories. They do not contact Docker, require root, or change the host.

## Scenario expectations

| Scenario | Expected result |
| --- | --- |
| Normal operation | `succeeded` after exact observation |
| Crash before Docker mutation | Proven absent; `retry_after_validation` |
| Docker success before receipt completion | Reconciliation confirms the exact object and completes the receipt |
| Docker failure before mutation | `failed`; safe only after fresh validation |
| Unknown Docker outcome | No blind retry; inspect first |
| Exact replay | Return the same receipt without another mutation |
| Conflicting operation ID | Reject with `OperationConflict` |
| Same-instance concurrency | Reject the second mutation with `InstanceBusy` |
| Different-instance concurrency | Permit both operations |
| Missing resource | Report `MissingResource`; do not recreate automatically |
| Foreign replacement | Report `OwnershipConflict`; block mutation |
| Labels without helper state | Report `OwnershipConflict`; do not adopt |
| Helper state without labels | Report `OwnershipConflict`; block mutation |
| Helper state loss | Remove destructive authority even when Docker objects remain |
| API state loss | Preserve helper ownership but stop automatic desired-state action |
| Stale executing receipt | Reconcile observation before retry |
| Security drift | Report `SecurityDrift`; block mutation |
| Administrator supersession | Preserve the old receipt and append an audit event |

## Limitations

- The file store uses atomic replace, file and directory `fsync`, and `flock` to demonstrate required semantics. It is not a production database candidate.
- The fake Docker adapter cannot prove real Engine behavior. ADR-0004's real-host evidence remains authoritative for Docker semantics.
- The prototype does not implement update data migrations, destructive removal, cancellation, cross-host coordination, or a UI.
- Wall-clock timestamps support audit readability, not lease safety. A lease never expires into permission to mutate.
