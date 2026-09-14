# FreshRSS final acceptance follow-up — 2026-09-13

Result: **FAIL**

## Measured cases

- Temporary administrator setup/login through production routes: PASS.
- FreshRSS catalog list/detail: PASS.
- Digest-pinned install and helper receipt: PASS.
- Container security shape (private bridge, no ports, no privilege, two
  approved mounts): PASS.
- Internal HTTP diagnostic: PASS (`302` followed by `200`).
- Exact reconciliation: PASS.
- Persistent marker creation and preservation through API remove: PASS.
- Authenticated stop/start/remove: PASS.
- KITPro control/helper backup sanity: PASS.
- Running foreign network member: PASS (`security_drift`).
- Stopped foreign member after the generic inspection fix: PASS
  (`security_drift`); removing the diagnostic container returned `exact`.
- FreshRSS runtime removal preserved the marker and storage root: PASS.
- Reinstall succeeded, but generated a new instance ID and therefore a new
  storage root. The previous marker was not reused by that newly generated
  instance: FAIL for the required storage-reuse acceptance.

## Remaining blockers

The remaining blocker is the production reinstall semantics: the API always
generates a new instance ID, so reinstall does not reuse the prior
instance-derived storage. The broader platform restart/reboot/missing-resource
certifications remain covered by their existing immutable production-slice
records and were intentionally not repeated here. No accepted security
decision was changed. Disposable FreshRSS runtime, storage, and temporary auth
state were removed afterward; Docker and KITPro services remain healthy.
