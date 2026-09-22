# Recover a lifecycle operation

Use this runbook when an operation remains accepted, the helper reports
`action_required`, reconciliation reports drift, or a restore was interrupted.
It applies to the Docker-based Debian and Arch public baseline. Rocky Linux and
Podman remain Experimental.

This runbook recovers recorded lifecycle state on the same host and
installation. It does not provide host-to-host restore, bare-host recovery, or
automatic rollback of irreversible upstream application migrations.

## Keep the consistency boundary intact

Do not delete helper database rows, rename managed storage trees, remove a
container by name, or start a retained generation manually. The helper database
is the authority for operation leases, fencing tokens, ownership, generations,
and restore journals. Runtime labels and the control database cannot recreate
that authority.

An HTTP timeout or closed browser does not cancel privileged work. Reuse the
same operation resource and inspect the helper result before starting another
mutation.

## Collect the current evidence

1. Record the KITPro version, source commit, installation ID, and operation ID.
2. Inspect the operation through `GET /api/v1/operations/<operation-id>`.
3. Inspect the latest result through
   `GET /api/v1/installations/<installation-id>/reconciliation`.
4. Check the services and recent helper messages:

   ```sh
   sudo systemctl status kitpro-api.service kitpro-helper.socket
   sudo journalctl -u kitpro-helper.service --since "30 minutes ago"
   docker info
   ```

5. Preserve `/var/lib/kitpro-helper/helper.db`, the control database, relevant
   application backups, and any `.kitpro-restore-*` trees before manual host
   recovery. Do not copy live SQLite files without using the packaged backup or
   SQLite-safe backup process.

## Interpret reconciliation

| State | Meaning | Normal next step |
| --- | --- | --- |
| `consistent` | The recorded generation and fresh runtime observation agree. | No repair. Verify the application separately. |
| `repairable` | The helper proved one bounded repair is safe. | Run only the returned recommended action. |
| `degraded` | A non-authoritative or retained resource differs. | Preserve evidence; reconcile and review the mismatch codes. |
| `action_required` | Ownership, configuration, dependency, or restore evidence is ambiguous. | Stop automatic retries and investigate. |
| `runtime_missing` | The active runtime is absent but trusted desired state remains. | Use controlled `recreate_generation` only when recommended. |
| `runtime_unknown` | The runtime could not be observed. | Restore Docker availability, then reconcile again. Do not mutate blindly. |
| `cleanup_pending` | The active generation committed, but exact old resources remain. | Use `cleanup_resources` only when recommended. |

Runtime state and application readiness are different. `running` proves the
container process state only. Open the application or use an application-aware
check before treating service as restored.

## Apply a bounded repair

The repair endpoint accepts only these actions and the helper revalidates each
one against fresh evidence:

- `start_active`: start the exact stopped active generation in dependency order;
- `recreate_generation`: create a new generation from trusted installation and
  catalog state when the active runtime is missing;
- `cleanup_resources`: remove exact non-active runtime resources without
  deleting managed or imported storage; and
- `acknowledge_retained_missing`: record that a retained generation is already
  gone so the helper no longer promises it for recovery.

Submit the action to
`POST /api/v1/installations/<installation-id>/repair` as
`{"action":"<recommended-action>"}` through an authenticated, CSRF-protected
administrator session. Poll the returned operation. Do not substitute a new
action after a transport failure.

## Recover an interrupted restore

Helper startup reads the durable restore journal before allowing another
lifecycle mutation. It verifies exact path identity and then completes a proven
forward swap, rolls back to the prior tree, records cleanup debt, or leaves the
operation action-required. `RestoreRecoveryRequired` means the journal is not
safe to bypass.

For an action-required restore, preserve all active, staged, rollback, and
failed trees and the helper database. Do not rename or merge them. Record each
path's device, inode, owner, mode, and checksum inventory, then report the
evidence for a reviewed recovery decision.

## Release a recovery lock as failed

If investigation proves that no automated repair should continue, a root
operator can close an action-required operation and release its lease:

```sh
sudo /usr/libexec/kitpro-helper \
  --resolve-operation <operation-id> --release-as-failed
```

This command records failure. It does not repair runtime state, delete cleanup
debt, or claim the requested result occurred. Reconcile the installation again
before any new mutation.

## Escalate instead of repairing

Stop and preserve evidence when runtime identity changed unexpectedly, a
container with the deterministic name has a different ID or configuration,
managed storage identity is mixed, dependency state is inconsistent, the
helper database fails integrity checks, or a repair action is not the one
recommended by fresh reconciliation.
