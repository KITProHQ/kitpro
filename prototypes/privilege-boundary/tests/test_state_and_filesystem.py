from __future__ import annotations

import json
import os
import tempfile
import time
import unittest
import uuid
from pathlib import Path

from fixture.protocol import ProtocolError, parse_request
from fixture.safe_fs import TestStorage
from fixture.state import StateStore


def parsed(operation_id: str, slot: str = "data"):
    return parse_request(
        json.dumps(
            {
                "protocol_major": 1,
                "protocol_minor": 0,
                "request_id": str(uuid.uuid4()),
                "operation_id": operation_id,
                "operation_kind": "PrepareTestDirectory",
                "operation_revision": 1,
                "expires_at": int(time.time()) + 60,
                "parameters": {"slot_id": slot},
            }
        ).encode()
    )


class StateTests(unittest.TestCase):
    def test_receipt_survives_restart_and_binds_content(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary)
            os.chmod(path, 0o700)
            state_file = path / "state.json"
            operation_id = str(uuid.uuid4())
            request = parsed(operation_id)
            first = StateStore(state_file, "run-test")
            self.assertIsNone(first.begin(request, os.getuid()))
            first.finish(request, result={"created": True})
            restarted = StateStore(state_file, "run-test")
            replay = restarted.begin(request, os.getuid())
            self.assertEqual(replay["result"], {"created": True})
            changed = parsed(operation_id, slot="other")
            with self.assertRaises(ProtocolError) as caught:
                restarted.begin(changed, os.getuid())
            self.assertEqual(caught.exception.code, "OperationConflict")

    def test_running_receipt_requires_recovery_after_restart(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary)
            os.chmod(path, 0o700)
            state_file = path / "state.json"
            request = parsed(str(uuid.uuid4()))
            StateStore(state_file, "run-test").begin(request, os.getuid())
            restarted = StateStore(state_file, "run-test")
            with self.assertRaises(ProtocolError) as caught:
                restarted.begin(request, os.getuid())
            self.assertEqual(caught.exception.code, "RecoveryRequired")


class FilesystemTests(unittest.TestCase):
    def test_safe_directory_creation_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "root"
            with TestStorage(root) as storage:
                first = storage.prepare("data")
                second = storage.prepare("data")
            self.assertTrue(first.created)
            self.assertFalse(second.created)
            self.assertEqual(first.inode, second.inode)
            self.assertEqual(first.mode, 0o700)

    def test_path_traversal_and_absolute_paths_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "root"
            with TestStorage(root) as storage:
                for path in ("../escape", "/absolute", "a/b"):
                    with self.subTest(path=path), self.assertRaises(ProtocolError):
                        storage.prepare(path)
            self.assertFalse((Path(temporary) / "escape").exists())

    def test_symlink_escape_and_replacement_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "root"
            outside = base / "outside"
            outside.mkdir()
            with TestStorage(root) as storage:
                (root / "link").symlink_to(outside, target_is_directory=True)
                with self.assertRaises(ProtocolError) as caught:
                    storage.prepare("link")
                self.assertEqual(caught.exception.code, "ForbiddenPath")
                storage.prepare("replace")
                (root / "replace").rmdir()
                (root / "replace").symlink_to(outside, target_is_directory=True)
                with self.assertRaises(ProtocolError) as caught:
                    storage.prepare("replace")
                self.assertEqual(caught.exception.code, "ForbiddenPath")


if __name__ == "__main__":
    unittest.main()
