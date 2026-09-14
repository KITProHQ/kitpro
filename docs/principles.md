# Product principles

These principles are requirements, not preferences. Architecture decisions and product work must satisfy all eight. If a proposal conflicts with one, the proposal must change or the project must record an explicit change to this document before implementation.

## 1. Local-first

Core server functions must not require KITPro cloud services. Installation, local access, application lifecycle controls, health, logs, and local recovery must continue when internet access or KITPro services are unavailable.

## 2. No lock-in

Applications and user data must remain usable without KITPro. Removing KITPro must not make standard workloads unreadable or require a KITPro service to recover the user's data.

## 3. Open foundations

KITPro should prefer established Linux facilities, documented interfaces, and open standards. A new project-specific mechanism needs a concrete benefit that existing tools cannot provide safely.

## 4. Secure defaults

Convenience must not silently reduce security. The product must make trust boundaries, exposed services, credentials, and privileged operations explicit. An unsafe override, if one exists, must require a deliberate action and explain its effect.

## 5. Inspectability

Advanced users must be able to understand what KITPro generated or changed. Configuration, operation history, errors, and relevant underlying tool output should be available in a form that a person can inspect.

Inspectability does not require exposing every internal detail in the default interface. It requires a reliable path from a high-level action to the resulting host and workload changes.

## 6. Reversibility

KITPro operations should be recoverable or reversible where practical. Before a destructive or difficult-to-reverse action, the product must identify the affected resources and require clear user intent.

A rollback claim must name what can be restored and the conditions under which restoration works. The interface must not imply that application binaries, configuration, database state, and user files all share the same rollback behavior when they do not.

## 7. Standard workloads

Applications must remain normal OCI containers, standard host services, or both. KITPro must not require proprietary workload formats or runtimes to keep an application running.

Project-specific metadata may describe or manage a workload, but it must not become the only usable representation of the application or its data.

## 8. Optional cloud

Future KITPro cloud services may add remote management, monitoring, backup coordination, or remote access. Loss of cloud connectivity must not make the user's local infrastructure unusable.

Cloud features must declare what data leaves the host, how the user disables the feature, and what remains functional after disconnection.

## Applying the principles

Each architecture decision record must state which principles constrain the decision. Each milestone review must also test the relevant principles against a working artifact, not only against design intent.

Use these questions during review:

- Does the core operation work without a KITPro account or cloud connection?
- Can the user continue to use the workload and data without KITPro?
- Does the design use a standard that independent tools understand?
- Does the default configuration avoid unnecessary exposure and privilege?
- Can the user inspect the action and its effects?
- Can the user recover from failure or reverse the action where practical?
- Does the workload remain a normal OCI container, a standard host service, or both?
- If a cloud feature fails, does local infrastructure continue to work?
