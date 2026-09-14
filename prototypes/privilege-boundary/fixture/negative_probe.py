"""Send fixed, non-destructive rejection probes to the disposable helper."""

from __future__ import annotations

import argparse
import json
import socket
import struct
import time
from pathlib import Path
from typing import Any

from .client import build_request, exchange
from .protocol import MAX_MESSAGE_BYTES, read_frame


def _raw(socket_path: Path, payload: bytes) -> dict[str, Any]:
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.settimeout(10)
    try:
        connection.connect(str(socket_path))
        connection.sendall(struct.pack(">I", len(payload)) + payload)
        response = read_frame(connection)
        if response is None:
            raise RuntimeError("helper disconnected")
        return json.loads(response)
    finally:
        connection.close()


def _request_with(operation: str, parameters: dict[str, Any]) -> dict[str, Any]:
    return build_request(operation, parameters)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", type=Path, required=True)
    arguments = parser.parse_args()

    probes: list[tuple[str, dict[str, Any]]] = [
        ("unknown operation", _request_with("RunCommand", {})),
        ("invalid instance", _request_with("InspectTestContainer", {"instance_id": "../escape"})),
        ("arbitrary Docker ID", _request_with("StopTestContainer", {"instance_id": "demo", "docker_id": "foreign"})),
        ("raw Docker request", _request_with("CreateTestContainer", {"instance_id": "demo", "docker_request": {"method": "DELETE", "path": "/containers/foreign"}})),
        ("Compose document", _request_with("CreateTestContainer", {"instance_id": "demo", "compose": {"services": {}}})),
        ("privileged", _request_with("CreateTestContainer", {"instance_id": "demo", "privileged": True})),
        ("host network", _request_with("CreateTestContainer", {"instance_id": "demo", "network_mode": "host"})),
        ("host PID", _request_with("CreateTestContainer", {"instance_id": "demo", "pid_mode": "host"})),
        ("host IPC", _request_with("CreateTestContainer", {"instance_id": "demo", "ipc_mode": "host"})),
        ("Docker socket mount", _request_with("CreateTestContainer", {"instance_id": "demo", "mounts": ["/var/run/docker.sock:/var/run/docker.sock"]})),
        ("root bind", _request_with("CreateTestContainer", {"instance_id": "demo", "binds": ["/:/host"]})),
        ("etc path", _request_with("CreateTestContainer", {"instance_id": "demo", "host_path": "/etc"})),
        ("proc path", _request_with("CreateTestContainer", {"instance_id": "demo", "host_path": "/proc"})),
        ("sys path", _request_with("CreateTestContainer", {"instance_id": "demo", "host_path": "/sys"})),
        ("dev path", _request_with("CreateTestContainer", {"instance_id": "demo", "host_path": "/dev"})),
        ("device", _request_with("CreateTestContainer", {"instance_id": "demo", "devices": ["/dev/sda"]})),
        ("capability", _request_with("CreateTestContainer", {"instance_id": "demo", "cap_add": ["SYS_ADMIN"]})),
        ("security option", _request_with("CreateTestContainer", {"instance_id": "demo", "security_opt": ["label=disable"]})),
        ("sysctl", _request_with("CreateTestContainer", {"instance_id": "demo", "sysctls": {"net.ipv4.ip_forward": "1"}})),
        ("arbitrary path", _request_with("PrepareTestDirectory", {"slot_id": "/tmp/outside"})),
    ]

    failures = 0
    for name, request in probes:
        response = exchange(arguments.socket, request)
        accepted = response.get("status") != "error"
        print(json.dumps({"probe": name, "accepted": accepted, "response": response}, sort_keys=True))
        failures += int(accepted)

    malformed = _raw(arguments.socket, b"not-json")
    print(json.dumps({"probe": "malformed JSON", "accepted": malformed.get("status") != "error", "response": malformed}, sort_keys=True))
    failures += int(malformed.get("status") != "error")

    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.settimeout(10)
    try:
        connection.connect(str(arguments.socket))
        connection.sendall(struct.pack(">I", MAX_MESSAGE_BYTES + 1))
        payload = read_frame(connection)
        oversized = json.loads(payload) if payload else {}
    finally:
        connection.close()
    print(json.dumps({"probe": "oversized request", "accepted": oversized.get("status") != "error", "response": oversized}, sort_keys=True))
    failures += int(oversized.get("status") != "error")

    stale = build_request("Ping", {})
    stale["expires_at"] = int(time.time()) - 1
    stale_response = exchange(arguments.socket, stale)
    print(json.dumps({"probe": "stale request", "accepted": stale_response.get("status") != "error", "response": stale_response}, sort_keys=True))
    failures += int(stale_response.get("status") != "error")

    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
