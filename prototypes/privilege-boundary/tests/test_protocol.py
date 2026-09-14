from __future__ import annotations

import json
import socket
import struct
import time
import unittest
import uuid

from fixture.protocol import (
    MAX_MESSAGE_BYTES,
    ProtocolError,
    encode_message,
    parse_request,
    read_frame,
)


def request(operation: str = "Ping", parameters: dict | None = None) -> dict:
    return {
        "protocol_major": 1,
        "protocol_minor": 0,
        "request_id": str(uuid.uuid4()),
        "operation_id": str(uuid.uuid4()),
        "operation_kind": operation,
        "operation_revision": 1,
        "expires_at": int(time.time()) + 60,
        "parameters": parameters or {},
    }


class ProtocolTests(unittest.TestCase):
    def test_valid_ping(self) -> None:
        parsed = parse_request(json.dumps(request()).encode())
        self.assertEqual(parsed.operation_kind, "Ping")

    def test_unknown_operation_is_rejected(self) -> None:
        value = request("RunCommand")
        with self.assertRaisesRegex(ProtocolError, "unsupported operation"):
            parse_request(json.dumps(value).encode())

    def test_unknown_and_missing_fields_are_rejected(self) -> None:
        value = request()
        value["docker_request"] = {"method": "DELETE"}
        with self.assertRaises(ProtocolError) as caught:
            parse_request(json.dumps(value).encode())
        self.assertEqual(caught.exception.code, "UnknownField")
        value = request()
        del value["operation_id"]
        with self.assertRaises(ProtocolError) as caught:
            parse_request(json.dumps(value).encode())
        self.assertEqual(caught.exception.code, "MissingField")

    def test_duplicate_json_keys_are_rejected(self) -> None:
        payload = b'{"protocol_major":1,"protocol_major":1}'
        with self.assertRaises(ProtocolError) as caught:
            parse_request(payload)
        self.assertEqual(caught.exception.code, "MalformedMessage")

    def test_invalid_utf8_and_nonobject_json_are_rejected(self) -> None:
        for payload in (b"\xff", b"[]", b"null"):
            with self.subTest(payload=payload), self.assertRaises(ProtocolError) as caught:
                parse_request(payload)
            self.assertEqual(caught.exception.code, "MalformedMessage")

    def test_invalid_instance_identifiers_are_rejected(self) -> None:
        for value in ("../escape", "/absolute", "Upper", "a" * 33, ""):
            with self.subTest(value=value), self.assertRaises(ProtocolError):
                parse_request(
                    json.dumps(request("InspectTestContainer", {"instance_id": value})).encode()
                )

    def test_dangerous_docker_attributes_are_rejected_before_dispatch(self) -> None:
        dangerous = {
            "privileged": True,
            "network_mode": "host",
            "pid_mode": "host",
            "ipc_mode": "host",
            "mounts": ["/var/run/docker.sock:/var/run/docker.sock"],
            "binds": ["/:/host"],
            "host_path": "/etc",
            "devices": ["/dev/sda"],
            "cap_add": ["SYS_ADMIN"],
            "security_opt": ["label=disable"],
            "sysctls": {"kernel.hostname": "changed"},
            "docker_args": ["run", "--privileged"],
            "docker_request": {"method": "DELETE", "path": "/containers/foreign"},
            "compose": {"services": {"attack": {"privileged": True}}},
            "command": ["sh", "-c", "id"],
            "environment": {"LD_PRELOAD": "/tmp/attack.so"},
        }
        for key, value in dangerous.items():
            payload = request("CreateTestContainer", {"instance_id": "demo", key: value})
            with self.subTest(key=key), self.assertRaises(ProtocolError) as caught:
                parse_request(json.dumps(payload).encode())
            self.assertIn(caught.exception.code, {"ForbiddenAttribute", "UnknownField"})

    def test_stale_and_excessive_deadlines_are_rejected(self) -> None:
        stale = request()
        stale["expires_at"] = int(time.time()) - 1
        with self.assertRaises(ProtocolError) as caught:
            parse_request(json.dumps(stale).encode())
        self.assertEqual(caught.exception.code, "DeadlineExceeded")
        future = request()
        future["expires_at"] = int(time.time()) + 301
        with self.assertRaises(ProtocolError) as caught:
            parse_request(json.dumps(future).encode())
        self.assertEqual(caught.exception.code, "InvalidArgument")

    def test_oversized_frame_is_rejected_without_reading_body(self) -> None:
        server, client = socket.socketpair()
        try:
            client.sendall(struct.pack(">I", MAX_MESSAGE_BYTES + 1))
            with self.assertRaises(ProtocolError) as caught:
                read_frame(server)
            self.assertEqual(caught.exception.code, "MessageTooLarge")
        finally:
            server.close()
            client.close()

    def test_frame_round_trip(self) -> None:
        server, client = socket.socketpair()
        try:
            framed = encode_message({"answer": 42})
            client.sendall(framed)
            self.assertEqual(read_frame(server), b'{"answer":42}')
        finally:
            server.close()
            client.close()


if __name__ == "__main__":
    unittest.main()
