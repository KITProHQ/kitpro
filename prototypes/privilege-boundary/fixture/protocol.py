"""Strict framed protocol for the disposable privilege-boundary fixture."""

from __future__ import annotations

import hashlib
import json
import re
import socket
import struct
import time
import uuid
from dataclasses import dataclass
from typing import Any


MAX_MESSAGE_BYTES = 1024 * 1024
PROTOCOL_MAJOR = 1
PROTOCOL_MINOR = 0
MAX_DEADLINE_SECONDS = 300

INSTANCE_ID = re.compile(r"^[a-z][a-z0-9-]{0,31}$")
SLOT_ID = re.compile(r"^[a-z][a-z0-9-]{0,31}$")

OPERATION_PARAMETERS: dict[str, frozenset[str]] = {
    "Ping": frozenset(),
    "InspectRuntime": frozenset(),
    "CreateTestContainer": frozenset({"instance_id"}),
    "InspectTestContainer": frozenset({"instance_id"}),
    "StartTestContainer": frozenset({"instance_id"}),
    "StopTestContainer": frozenset({"instance_id"}),
    "RemoveTestContainer": frozenset({"instance_id"}),
    "PrepareTestDirectory": frozenset({"slot_id"}),
    "GetOperation": frozenset({"subject_operation_id"}),
}

MUTATING_OPERATIONS = frozenset(
    {
        "CreateTestContainer",
        "StartTestContainer",
        "StopTestContainer",
        "RemoveTestContainer",
        "PrepareTestDirectory",
    }
)

FORBIDDEN_PARAMETER_KEYS = frozenset(
    {
        "binds",
        "cap_add",
        "capabilities",
        "command",
        "compose",
        "devices",
        "docker_args",
        "docker_id",
        "docker_request",
        "docker_socket",
        "entrypoint",
        "env",
        "environment",
        "host_config",
        "host_path",
        "ipc_mode",
        "mounts",
        "network_mode",
        "pid_mode",
        "privileged",
        "security_opt",
        "sysctls",
    }
)

REQUEST_FIELDS = frozenset(
    {
        "protocol_major",
        "protocol_minor",
        "request_id",
        "operation_id",
        "operation_kind",
        "operation_revision",
        "expires_at",
        "parameters",
    }
)


class ProtocolError(Exception):
    """A safe error that can cross the fixture socket."""

    def __init__(self, code: str, message: str, *, retryable: bool = False):
        super().__init__(message)
        self.code = code
        self.message = message[:500]
        self.retryable = retryable


@dataclass(frozen=True)
class Request:
    protocol_major: int
    protocol_minor: int
    request_id: str
    operation_id: str
    operation_kind: str
    operation_revision: int
    expires_at: int
    parameters: dict[str, Any]

    def semantic_digest(self) -> str:
        semantic = {
            "protocol_major": self.protocol_major,
            "protocol_minor": self.protocol_minor,
            "operation_kind": self.operation_kind,
            "operation_revision": self.operation_revision,
            "parameters": self.parameters,
        }
        encoded = json.dumps(
            semantic, sort_keys=True, separators=(",", ":"), ensure_ascii=True
        ).encode("utf-8")
        return "sha256:" + hashlib.sha256(encoded).hexdigest()


def _reject_duplicate_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ProtocolError("MalformedMessage", f"duplicate key: {key}")
        result[key] = value
    return result


def decode_json(payload: bytes) -> dict[str, Any]:
    try:
        text = payload.decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise ProtocolError("MalformedMessage", "request is not valid UTF-8") from exc

    try:
        value = json.loads(text, object_pairs_hook=_reject_duplicate_pairs)
    except ProtocolError:
        raise
    except (json.JSONDecodeError, ValueError) as exc:
        raise ProtocolError("MalformedMessage", "request is not valid JSON") from exc

    if not isinstance(value, dict):
        raise ProtocolError("MalformedMessage", "request must be a JSON object")
    return value


def _require_exact_fields(value: dict[str, Any], expected: frozenset[str]) -> None:
    actual = frozenset(value)
    unknown = sorted(actual - expected)
    missing = sorted(expected - actual)
    if unknown:
        raise ProtocolError("UnknownField", f"unknown field: {unknown[0]}")
    if missing:
        raise ProtocolError("MissingField", f"missing field: {missing[0]}")


def _require_uuid(value: Any, field: str) -> str:
    if not isinstance(value, str):
        raise ProtocolError("InvalidIdentifier", f"{field} must be a UUID string")
    try:
        parsed = uuid.UUID(value)
    except ValueError as exc:
        raise ProtocolError("InvalidIdentifier", f"{field} must be a UUID string") from exc
    if str(parsed) != value.lower():
        raise ProtocolError("InvalidIdentifier", f"{field} must use canonical UUID form")
    return value.lower()


def _scan_forbidden_keys(value: Any) -> None:
    if isinstance(value, dict):
        for key, nested in value.items():
            normalized = key.lower().replace("-", "_")
            if normalized in FORBIDDEN_PARAMETER_KEYS:
                raise ProtocolError(
                    "ForbiddenAttribute", f"forbidden parameter: {key}"
                )
            _scan_forbidden_keys(nested)
    elif isinstance(value, list):
        for nested in value:
            _scan_forbidden_keys(nested)


def parse_request(payload: bytes, *, now: int | None = None) -> Request:
    value = decode_json(payload)
    _require_exact_fields(value, REQUEST_FIELDS)

    if value["protocol_major"] != PROTOCOL_MAJOR:
        raise ProtocolError("UnsupportedProtocol", "unsupported protocol major version")
    if value["protocol_minor"] != PROTOCOL_MINOR:
        raise ProtocolError("UnsupportedProtocol", "unsupported protocol minor version")
    if value["operation_revision"] != 1:
        raise ProtocolError("UnsupportedOperation", "unsupported operation revision")

    operation = value["operation_kind"]
    if not isinstance(operation, str) or operation not in OPERATION_PARAMETERS:
        raise ProtocolError("UnsupportedOperation", "unsupported operation")

    request_id = _require_uuid(value["request_id"], "request_id")
    operation_id = _require_uuid(value["operation_id"], "operation_id")

    expires_at = value["expires_at"]
    if isinstance(expires_at, bool) or not isinstance(expires_at, int):
        raise ProtocolError("InvalidArgument", "expires_at must be an integer")
    current = int(time.time()) if now is None else now
    if expires_at < current:
        raise ProtocolError("DeadlineExceeded", "request has expired")
    if expires_at > current + MAX_DEADLINE_SECONDS:
        raise ProtocolError("InvalidArgument", "request deadline is too far in the future")

    parameters = value["parameters"]
    if not isinstance(parameters, dict):
        raise ProtocolError("InvalidArgument", "parameters must be an object")
    _scan_forbidden_keys(parameters)
    _require_exact_fields(parameters, OPERATION_PARAMETERS[operation])

    if "instance_id" in parameters:
        instance_id = parameters["instance_id"]
        if not isinstance(instance_id, str) or not INSTANCE_ID.fullmatch(instance_id):
            raise ProtocolError("InvalidIdentifier", "invalid instance_id")
    if "slot_id" in parameters:
        slot_id = parameters["slot_id"]
        if not isinstance(slot_id, str) or not SLOT_ID.fullmatch(slot_id):
            raise ProtocolError("InvalidIdentifier", "invalid slot_id")
    if "subject_operation_id" in parameters:
        parameters = dict(parameters)
        parameters["subject_operation_id"] = _require_uuid(
            parameters["subject_operation_id"], "subject_operation_id"
        )

    return Request(
        protocol_major=PROTOCOL_MAJOR,
        protocol_minor=PROTOCOL_MINOR,
        request_id=request_id,
        operation_id=operation_id,
        operation_kind=operation,
        operation_revision=1,
        expires_at=expires_at,
        parameters=parameters,
    )


def encode_message(value: dict[str, Any]) -> bytes:
    payload = json.dumps(
        value, sort_keys=True, separators=(",", ":"), ensure_ascii=True
    ).encode("utf-8")
    if len(payload) > MAX_MESSAGE_BYTES:
        raise ProtocolError("MessageTooLarge", "response exceeds message limit")
    return struct.pack(">I", len(payload)) + payload


def read_frame(connection: socket.socket) -> bytes | None:
    header = _read_exact(connection, 4, allow_clean_eof=True)
    if header is None:
        return None
    size = struct.unpack(">I", header)[0]
    if size == 0:
        raise ProtocolError("MalformedMessage", "empty request")
    if size > MAX_MESSAGE_BYTES:
        raise ProtocolError("MessageTooLarge", "request exceeds message limit")
    payload = _read_exact(connection, size, allow_clean_eof=False)
    if payload is None:
        raise ProtocolError("MalformedMessage", "truncated request")
    return payload


def _read_exact(
    connection: socket.socket, count: int, *, allow_clean_eof: bool
) -> bytes | None:
    chunks: list[bytes] = []
    remaining = count
    while remaining:
        chunk = connection.recv(remaining)
        if not chunk:
            if allow_clean_eof and not chunks:
                return None
            raise ProtocolError("MalformedMessage", "truncated request")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def success_response(request: Request, result: dict[str, Any]) -> dict[str, Any]:
    return {
        "protocol_major": PROTOCOL_MAJOR,
        "protocol_minor": PROTOCOL_MINOR,
        "request_id": request.request_id,
        "operation_id": request.operation_id,
        "status": "ok",
        "result": result,
    }


def error_response(
    error: ProtocolError,
    *,
    request_id: str | None = None,
    operation_id: str | None = None,
) -> dict[str, Any]:
    return {
        "protocol_major": PROTOCOL_MAJOR,
        "protocol_minor": PROTOCOL_MINOR,
        "request_id": request_id,
        "operation_id": operation_id,
        "status": "error",
        "error": {
            "code": error.code,
            "message": error.message,
            "retryable": error.retryable,
        },
    }
