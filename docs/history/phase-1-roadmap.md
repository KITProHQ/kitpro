# Historical Phase 1 roadmap

> Historical document. This plan predates the implemented KITPro Server alpha.
> It is preserved to show the original development sequence. It does not
> describe the current product. See the [current roadmap](../roadmap.md) and
> [alpha.12 current state](../product/kitpro-server-current-state.md).

This roadmap ends with one complete KITPro Server application-management slice. The development sequence starts with architecture and security decisions. It favors a narrow, proven lifecycle over broad distribution or a large catalog.

No milestone may add a mandatory account, cloud control plane, proprietary runtime, custom hardware dependency, or automatic deletion of persistent user data.

## Milestone 0: Record the product foundation

Outcome: Before development starts, the repository records the mission, Phase 1 boundary, product principles, open architecture decisions, and milestone sequence.

Exit criteria:

- The root and product-area README files describe current scope.
- The vision and principles are reviewable.
- The architecture document names all blocking decisions without presenting guesses as decisions.
- The decision-record process exists.

## Milestone 1: Define supported use and security boundaries

Outcome: The project knows which host and threat model the first implementation must support.

Current status: Josh approved Debian 13 as the Phase 1 primary/reference host and Rocky Linux 10 as secondary/experimental. [ADR-0017](../decisions/0017-phase-1-host-compatibility.md) is accepted. [ADR-0018](../decisions/0018-security-boundaries.md) remains proposed until its remaining production-design gates pass.

Work:

- Select the first supported Linux distribution, release, CPU architecture, init system, and minimum host resources.
- Define local, LAN, and any deferred remote-access assumptions.
- Create the threat model and identify protected assets, actors, trust boundaries, and abuse cases.
- List every Phase 1 operation that may need elevated privilege.
- Define installation, upgrade, recovery, and removal expectations for KITPro Server itself.

Exit criteria:

- ADR-0017 is accepted after owner review. ADR-0018 remains a required pre-production decision.
- The project has a testable supported-host statement.
- The threat model covers a compromised managed application and an unauthorized LAN client.
- The privilege-boundary prototype passes the tests required by ADR-0018.

## Milestone 2: Decide the core system shape

Outcome: The implementation has clear component, privilege, API, and persistence boundaries.

Current status: Platform selection and real-host validation are complete. Josh approved [ADR-0017](../decisions/0017-phase-1-host-compatibility.md). The privilege-boundary fixture passes 35 repository tests, and the core boundary passed on both candidate hosts. [ADR-0016](../decisions/0016-durable-state-and-reconciliation.md) and [ADR-0019](../decisions/0019-helper-mandatory-access-control.md) are accepted. ADR-0016 defines durable receipts, bounded reconciliation, and fenced per-instance leases; ADR-0019 requires enforcing helper MAC. The disposable fixtures pass 20 reconciliation scenarios and both platform MAC feasibility runs. No production code has started.

Platform-specific remaining tests are in the [Debian security-validation backlog](../follow-ups/debian-security-validation.md) and [Rocky experimental-support backlog](../follow-ups/rocky-experimental-support.md). Rocky work does not block the Debian 13 Phase 1 path.

Work:

Complete the remaining pre-production work in this order:

1. Maintain the accepted durable operation receipts, ownership state, restart recovery, and reconciliation model in ADR-0016.
2. ADR-0019 is accepted: Debian requires enforcing AppArmor; Rocky remains experimental pending production policy lifecycle.
3. Review the production systemd unit and resolve the `RestrictSUIDSGID` and `openat2` interaction.
4. The implementation stack, state database, protocol serialization, and frontend ADRs are accepted. Go helper/runtime and migration/backup probes pass locally; Debian AppArmor/systemd, consistent backup under the full helper, and production-shaped VM gates remain explicit before source-tree creation.
5. Begin the first vertical-slice implementation only after the remaining implementation-specific validation gates pass.

Exit criteria:

- ADR-0001, ADR-0002, ADR-0003, ADR-0004, ADR-0015, ADR-0016, ADR-0019, ADR-0020, and ADR-0021 are accepted. Production packaging must still prove the accepted MAC requirements.
- A design walk-through covers duplicate requests, partial failure, host restart, stale observed state, and conflicting operations.
- No browser connection is required to keep an accepted operation running.

## Milestone 3: Specify one standard application

Outcome: One real application has a complete, inspectable definition that does not replace its standard workload format.

Work:

- Apply the accepted Docker integration and version policy to one real application definition. Revisit the host decision if the tested runtime boundary does not work on the selected host.
- Define the application specification and its relationship to the underlying workload definition.
- Define storage, secrets, network exposure, health, logs, versions, update, rollback, backup, and uninstall behavior for the application.
- Select the first supported application for engineering coverage, not catalog appeal.

Exit criteria:

- ADR-0005, ADR-0006, ADR-0007, ADR-0010, ADR-0011, ADR-0012, ADR-0013, and ADR-0014 are accepted.
- The specification can represent the first application's full lifecycle without application-specific code in the lifecycle coordinator.
- An operator can identify the standard workload, persistent data, generated configuration, and secrets policy from the definition.

## Milestone 4: Prove installation and local access

Outcome: A user can install KITPro Server on a clean supported host and open the authenticated local dashboard.

Work:

- Implement repeatable installation and removal on the selected host.
- Implement the selected authentication and local TLS models.
- Show the installed KITPro version and service health.
- Document recovery from an interrupted installation and lost local credentials.

Exit criteria:

- ADR-0008 and ADR-0009 are accepted.
- Tests start from a clean supported host image.
- The dashboard does not require a KITPro account or KITPro cloud connection.
- Removal identifies any retained configuration or state before changing the host.

## Milestone 5: Detect and explain the host

Outcome: The dashboard reports the host facts needed to decide whether the first application can run.

Work:

- Detect the supported operating system, architecture, resources, storage, runtime, and relevant network conflicts.
- Distinguish unsupported, unavailable, degraded, and healthy states.
- Show where each important fact came from.

Exit criteria:

- The dashboard rejects an unsupported host before deployment changes it.
- Detection tests cover missing tools, insufficient resources, permission failures, and conflicting ports.
- Advanced users can inspect the underlying observations.

## Milestone 6: Deploy and operate one application

Outcome: A user can deploy the supported application, see its health, and start or stop it.

Work:

- Validate the deployment plan before applying it.
- Show the planned workload, storage, network exposure, and secrets handling.
- Record deployment progress and final state.
- Add runtime health and idempotent start and stop controls.

Exit criteria:

- A successful deployment survives a KITPro restart and a host restart.
- A failed deployment reports the failed step and leaves a defined recovery path.
- The dashboard distinguishes desired state from observed state.
- The resulting workload remains operable through standard host tools.

## Milestone 7: Add useful logs and diagnosis

Outcome: A user can inspect the information needed to understand a failed operation or unhealthy application.

Work:

- Correlate application logs, runtime state, health checks, and KITPro operation records.
- Apply bounded retention and secret-redaction rules.
- Explain unavailable logs without labeling the application unhealthy by assumption.

Exit criteria:

- Deployment, start, stop, and health failures link to relevant evidence.
- Logs remain available locally without cloud telemetry.
- Tests verify retention bounds and secret handling.

## Milestone 8: Update with a tested recovery path

Outcome: A user can update the supported application with clear preconditions and failure recovery.

Work:

- Detect and verify an available update.
- Present version, configuration, migration, downtime, data, backup, and rollback effects before approval.
- Execute the update as a recoverable operation.
- Test failures before, during, and after the workload change.

Exit criteria:

- The update never reports success before post-update health checks pass.
- The system can reconcile an interrupted update after restart.
- The rollback or recovery behavior matches the accepted ADR and the application's data limits.
- The interface does not claim that restoring an image also restores changed persistent data.

## Milestone 9: Uninstall without destroying data

Outcome: A user can remove the supported application while retaining persistent user data by default.

Work:

- Preview the managed components that uninstall will remove.
- Identify the data, backups, and user-managed resources that will remain.
- Remove the workload and KITPro-owned disposable configuration.
- Provide an inspectable record of the result and a documented reinstall path.

Exit criteria:

- Uninstall does not delete persistent user data.
- Repeating uninstall is safe or returns a clear no-op result.
- Partial failure leaves enough state to resume or repair the operation.
- The retained data remains accessible without KITPro.

## Milestone 10: Accept the complete vertical slice

Outcome: The entire Phase 1 workflow passes on a clean supported host as one product experience.

Exit criteria:

- A target user can install KITPro Server, open the dashboard, inspect the host, and deploy the supported application.
- The user can view health and logs, start and stop the application, update it, and uninstall it while retaining data.
- Offline tests prove that core operations do not require KITPro cloud services.
- Failure tests cover interruption, restart, insufficient resources, invalid input, runtime failure, unhealthy updates, and partial uninstall.
- Security review confirms the accepted threat model and privilege boundaries.
- The project documents known limits and defers broader catalogs, more host platforms, remote access, hardware, OS, cloud, and AI work.
