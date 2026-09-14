"""Root-owned state for the disposable privilege-boundary fixture."""

from __future__ import annotations

import copy
import json
import os
import stat
import tempfile
import threading
from pathlib import Path
from typing import Any, Callable

from .protocol import ProtocolError, Request


SCHEMA_VERSION = 1


class StateStore:
    def __init__(self, path: Path, run_id: str):
        self.path = path
        self.run_id = run_id
        self._lock = threading.RLock()
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self._verify_directory(self.path.parent)
        if not self.path.exists():
            self._write(
                {
                    "schema": SCHEMA_VERSION,
                    "run_id": run_id,
                    "operations": {},
                    "resources": {},
                }
            )
        self._load()

    @staticmethod
    def _verify_directory(path: Path) -> None:
        info = path.stat()
        if info.st_uid != os.geteuid():
            raise RuntimeError(f"state directory is not owned by effective UID: {path}")
        if stat.S_IMODE(info.st_mode) & 0o077:
            raise RuntimeError(f"state directory permissions are too broad: {path}")

    def _load(self) -> dict[str, Any]:
        with self.path.open("r", encoding="utf-8") as handle:
            value = json.load(handle)
        if not isinstance(value, dict):
            raise RuntimeError("state root must be an object")
        if value.get("schema") != SCHEMA_VERSION:
            raise RuntimeError("unsupported state schema")
        if value.get("run_id") != self.run_id:
            raise RuntimeError("state run_id does not match configured run_id")
        if not isinstance(value.get("operations"), dict):
            raise RuntimeError("state operations must be an object")
        if not isinstance(value.get("resources"), dict):
            raise RuntimeError("state resources must be an object")
        return value

    def _write(self, value: dict[str, Any]) -> None:
        encoded = json.dumps(
            value, sort_keys=True, indent=2, ensure_ascii=True
        ).encode("utf-8") + b"\n"
        descriptor, temporary = tempfile.mkstemp(
            prefix=".state-", dir=str(self.path.parent)
        )
        try:
            os.fchmod(descriptor, 0o600)
            with os.fdopen(descriptor, "wb", closefd=True) as handle:
                handle.write(encoded)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, self.path)
            directory = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        except BaseException:
            try:
                os.close(descriptor)
            except OSError:
                pass
            try:
                os.unlink(temporary)
            except FileNotFoundError:
                pass
            raise

    def _change(self, callback: Callable[[dict[str, Any]], Any]) -> Any:
        with self._lock:
            value = self._load()
            result = callback(value)
            self._write(value)
            return result

    def begin(self, request: Request, peer_uid: int) -> dict[str, Any] | None:
        digest = request.semantic_digest()

        def update(value: dict[str, Any]) -> dict[str, Any] | None:
            existing = value["operations"].get(request.operation_id)
            if existing is not None:
                if existing.get("request_digest") != digest:
                    raise ProtocolError(
                        "OperationConflict",
                        "operation_id is already bound to different request content",
                    )
                if existing.get("status") in {"completed", "failed"}:
                    return copy.deepcopy(existing)
                raise ProtocolError(
                    "RecoveryRequired",
                    "operation was interrupted before a final result was recorded",
                )
            value["operations"][request.operation_id] = {
                "request_digest": digest,
                "operation_kind": request.operation_kind,
                "peer_uid": peer_uid,
                "status": "running",
            }
            return None

        return self._change(update)

    def finish(
        self,
        request: Request,
        *,
        result: dict[str, Any] | None = None,
        error: dict[str, Any] | None = None,
    ) -> None:
        if (result is None) == (error is None):
            raise ValueError("exactly one of result or error is required")

        def update(value: dict[str, Any]) -> None:
            record = value["operations"].get(request.operation_id)
            if record is None or record.get("request_digest") != request.semantic_digest():
                raise RuntimeError("operation receipt is missing or changed")
            record["status"] = "completed" if result is not None else "failed"
            if result is not None:
                record["result"] = copy.deepcopy(result)
            else:
                record["error"] = copy.deepcopy(error)

        self._change(update)

    def get_operation(self, operation_id: str) -> dict[str, Any]:
        with self._lock:
            value = self._load()
            record = value["operations"].get(operation_id)
            if record is None:
                raise ProtocolError("NotFound", "operation was not found")
            safe = copy.deepcopy(record)
            safe.pop("peer_uid", None)
            return safe

    def get_resource(self, instance_id: str, kind: str) -> dict[str, Any] | None:
        with self._lock:
            value = self._load()
            instance = value["resources"].get(instance_id, {})
            record = instance.get(kind)
            return copy.deepcopy(record) if record is not None else None

    def put_resource(self, instance_id: str, kind: str, record: dict[str, Any]) -> None:
        if kind not in {"container", "network"}:
            raise ValueError("unsupported resource kind")

        def update(value: dict[str, Any]) -> None:
            instance = value["resources"].setdefault(instance_id, {})
            instance[kind] = copy.deepcopy(record)

        self._change(update)

    def remove_resource(self, instance_id: str, kind: str) -> None:
        def update(value: dict[str, Any]) -> None:
            instance = value["resources"].get(instance_id)
            if instance is None:
                return
            instance.pop(kind, None)
            if not instance:
                value["resources"].pop(instance_id, None)

        self._change(update)

    def snapshot(self) -> dict[str, Any]:
        with self._lock:
            return copy.deepcopy(self._load())
