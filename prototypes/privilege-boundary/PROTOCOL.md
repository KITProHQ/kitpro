# Experimental protocol contract

This is the test fixture's wire contract, not the final KITPro protocol schema.

## Transport and identity

- Linux Unix domain stream socket only.
- Four-byte unsigned big-endian payload length followed by UTF-8 JSON.
- Maximum payload size: 1 MiB.
- One request per connection in this fixture.
- The socket's owner, group, directory mode, and socket mode form the first access check.
- The helper reads `SO_PEERCRED` and compares the UID to one configured service identity.
- No shared secret is used.

## Request envelope

Every request has exactly these fields:

```json
{
  "protocol_major": 1,
  "protocol_minor": 0,
  "request_id": "canonical UUID",
  "operation_id": "canonical UUID",
  "operation_kind": "closed enum",
  "operation_revision": 1,
  "expires_at": 0,
  "parameters": {}
}
```

Unknown fields, duplicate JSON keys, noncanonical UUIDs, unknown operations, unknown revisions, expired requests, and deadlines over five minutes fail closed. `request_id` identifies one transport attempt. `operation_id` binds one semantic operation to its canonical request digest.

The fixture uses `expires_at` rather than the ADR's provisional `deadline` spelling. Production schema work must settle that name and reconcile the ADR before implementation.

## Operation bodies

| Operation | Exact `parameters` keys |
| --- | --- |
| `Ping` | None |
| `InspectRuntime` | None |
| `CreateTestContainer` | `instance_id` |
| `InspectTestContainer` | `instance_id` |
| `StartTestContainer` | `instance_id` |
| `StopTestContainer` | `instance_id` |
| `RemoveTestContainer` | `instance_id` |
| `PrepareTestDirectory` | `slot_id` |
| `GetOperation` | `subject_operation_id` |

Instance and slot IDs match `^[a-z][a-z0-9-]{0,31}$`. They are semantic names, not Docker or filesystem identifiers.

## Response envelope

Success:

```json
{
  "protocol_major": 1,
  "protocol_minor": 0,
  "request_id": "UUID",
  "operation_id": "UUID",
  "status": "ok",
  "result": {}
}
```

Failure:

```json
{
  "protocol_major": 1,
  "protocol_minor": 0,
  "request_id": null,
  "operation_id": null,
  "status": "error",
  "error": {
    "code": "stable machine code",
    "message": "bounded safe diagnostic",
    "retryable": false
  }
}
```

IDs are null if parsing failed before they were authenticated and validated.

## Replay behavior

- The first request stores `running` before dispatch.
- An exact replay of a completed or failed request returns the stored result.
- Reusing an operation ID for different semantic content returns `OperationConflict`.
- A helper restart preserves terminal receipts.
- A receipt left `running` returns `RecoveryRequired`.
- A stale request fails before a receipt or mutation.

This demonstrates binding and fail-closed recovery. Production needs step-level reconciliation for interrupted Docker operations.
