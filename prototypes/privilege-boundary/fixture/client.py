"""Typed client for the disposable privilege-boundary fixture."""

from __future__ import annotations

import argparse
import json
import socket
import sys
import time
import uuid
from pathlib import Path
from typing import Any

from .protocol import PROTOCOL_MAJOR, PROTOCOL_MINOR, encode_message, read_frame


def build_request(
    operation_kind: str,
    parameters: dict[str, Any],
    *,
    operation_id: str | None = None,
) -> dict[str, Any]:
    return {
        "protocol_major": PROTOCOL_MAJOR,
        "protocol_minor": PROTOCOL_MINOR,
        "request_id": str(uuid.uuid4()),
        "operation_id": operation_id or str(uuid.uuid4()),
        "operation_kind": operation_kind,
        "operation_revision": 1,
        "expires_at": int(time.time()) + 60,
        "parameters": parameters,
    }


def exchange(socket_path: Path, request: dict[str, Any]) -> dict[str, Any]:
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.settimeout(20.0)
    try:
        connection.connect(str(socket_path))
        connection.sendall(encode_message(request))
        payload = read_frame(connection)
        if payload is None:
            raise RuntimeError("helper disconnected without a response")
        value = json.loads(payload.decode("utf-8"))
        if not isinstance(value, dict):
            raise RuntimeError("helper response was not an object")
        return value
    finally:
        connection.close()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--operation-id")
    subparsers = parser.add_subparsers(dest="command", required=True)
    for command in ("ping", "inspect-runtime"):
        subparsers.add_parser(command)
    for command in ("create", "inspect", "start", "stop", "remove"):
        child = subparsers.add_parser(command)
        child.add_argument("instance_id")
    prepare = subparsers.add_parser("prepare-directory")
    prepare.add_argument("slot_id")
    get_operation = subparsers.add_parser("get-operation")
    get_operation.add_argument("subject_operation_id")
    arguments = parser.parse_args()

    operation_map = {
        "ping": "Ping",
        "inspect-runtime": "InspectRuntime",
        "create": "CreateTestContainer",
        "inspect": "InspectTestContainer",
        "start": "StartTestContainer",
        "stop": "StopTestContainer",
        "remove": "RemoveTestContainer",
        "prepare-directory": "PrepareTestDirectory",
        "get-operation": "GetOperation",
    }
    parameters: dict[str, Any] = {}
    if hasattr(arguments, "instance_id"):
        parameters["instance_id"] = arguments.instance_id
    if hasattr(arguments, "slot_id"):
        parameters["slot_id"] = arguments.slot_id
    if hasattr(arguments, "subject_operation_id"):
        parameters["subject_operation_id"] = arguments.subject_operation_id
    response = exchange(
        arguments.socket,
        build_request(
            operation_map[arguments.command],
            parameters,
            operation_id=arguments.operation_id,
        ),
    )
    print(json.dumps(response, indent=2, sort_keys=True))
    return 0 if response.get("status") == "ok" else 2


if __name__ == "__main__":
    sys.exit(main())
