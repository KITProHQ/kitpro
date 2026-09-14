from __future__ import annotations

import concurrent.futures
import json
import os
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import uuid
from pathlib import Path

from fixture.client import build_request, exchange
from fixture.helper import _handle_connection, _peer_credentials
from fixture.protocol import MAX_MESSAGE_BYTES, read_frame


IMAGE = "docker.io/library/busybox@sha256:" + "a" * 64


class SocketIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        os.chmod(self.root, 0o700)
        self.socket_path = self.root / "helper.sock"
        self.run_file = self.root / "run-id"
        self.image_file = self.root / "image"
        self.run_file.write_text("12345678-1234-4234-9234-123456789abc\n", encoding="utf-8")
        self.image_file.write_text(IMAGE + "\n", encoding="utf-8")
        os.chmod(self.run_file, 0o600)
        os.chmod(self.image_file, 0o600)
        self.process = None
        self._start()

    def tearDown(self):
        self._stop()
        self.temporary.cleanup()

    def _start(self):
        command = [
            sys.executable,
            "-m",
            "fixture.helper",
            "--standalone-test-socket",
            str(self.socket_path),
            "--allowed-uid",
            str(os.getuid()),
            "--state-file",
            str(self.root / "state" / "state.json"),
            "--run-id-file",
            str(self.run_file),
            "--image-file",
            str(self.image_file),
            "--storage-root",
            str(self.root / "storage"),
        ]
        self.process = subprocess.Popen(
            command,
            cwd=Path(__file__).parents[1],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        for _ in range(100):
            if self.socket_path.exists():
                return
            if self.process.poll() is not None:
                output = self.process.stdout.read() if self.process.stdout else ""
                self.fail(f"helper exited before listening: {output}")
            time.sleep(0.02)
        self.fail("helper socket did not appear")

    def _stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            self.process.wait(timeout=5)
        if self.process is not None and self.process.stdout is not None:
            self.process.stdout.close()

    def test_peer_credentials_report_kernel_identity(self):
        server, client = socket.socketpair()
        try:
            pid, uid, gid = _peer_credentials(server)
            self.assertEqual(pid, os.getpid())
            self.assertEqual(uid, os.getuid())
            self.assertEqual(gid, os.getgid())
        finally:
            server.close()
            client.close()

    def test_ping_and_socket_mode(self):
        response = exchange(self.socket_path, build_request("Ping", {}))
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["result"]["authenticated_peer_uid"], os.getuid())
        self.assertFalse(response["result"]["human_authorization_verified"])
        self.assertEqual(self.socket_path.stat().st_mode & 0o777, 0o600)

    def test_malformed_oversized_and_disconnected_clients_do_not_kill_helper(self):
        self.assertEqual(self._raw(b"not-json")["error"]["code"], "MalformedMessage")
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.connect(str(self.socket_path))
        connection.sendall(struct.pack(">I", MAX_MESSAGE_BYTES + 1))
        payload = read_frame(connection)
        connection.close()
        self.assertEqual(json.loads(payload)["error"]["code"], "MessageTooLarge")

        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.connect(str(self.socket_path))
        connection.sendall(struct.pack(">I", 200) + b"{")
        connection.close()
        time.sleep(0.05)
        self.assertEqual(exchange(self.socket_path, build_request("Ping", {}))["status"], "ok")

    def test_concurrent_requests(self):
        def ping(_index):
            return exchange(self.socket_path, build_request("Ping", {}))["status"]

        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            statuses = list(pool.map(ping, range(24)))
        self.assertEqual(statuses, ["ok"] * 24)

    def test_helper_and_client_restart_preserve_receipt(self):
        operation_id = str(uuid.uuid4())
        request = build_request("Ping", {}, operation_id=operation_id)
        first = exchange(self.socket_path, request)
        self._stop()
        self._start()
        replay = build_request("Ping", {}, operation_id=operation_id)
        second = exchange(self.socket_path, replay)
        self.assertEqual(first["result"], second["result"])

    def test_unauthorized_peer_is_rejected_before_request_parsing(self):
        server, client = socket.socketpair()

        class UnusedService:
            def execute(self, _request, _uid):
                raise AssertionError("unauthorized request reached service")

        thread = threading.Thread(
            target=_handle_connection, args=(server, UnusedService(), os.getuid() + 1)
        )
        thread.start()
        payload = read_frame(client)
        thread.join(timeout=2)
        client.close()
        self.assertEqual(json.loads(payload)["error"]["code"], "UnauthorizedCaller")

    def _raw(self, payload):
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            connection.connect(str(self.socket_path))
            connection.sendall(struct.pack(">I", len(payload)) + payload)
            response = read_frame(connection)
            return json.loads(response)
        finally:
            connection.close()


if __name__ == "__main__":
    unittest.main()
