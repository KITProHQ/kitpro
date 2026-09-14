"""Inspectable file stores used only by the reconciliation prototype."""

from __future__ import annotations

import fcntl
import json
import os
import tempfile
from copy import deepcopy
from pathlib import Path
from typing import Any, Callable

from .model import ReconciliationError


StateMutation = Callable[[dict[str, Any]], Any]


class JsonStore:
    """A locked, atomic JSON file for testing state transitions."""

    def __init__(self, path: Path, initial: Callable[[], dict[str, Any]]):
        self.path = path
        self.lock_path = path.with_suffix(path.suffix + ".lock")
        self._initial = initial
        path.parent.mkdir(parents=True, exist_ok=True)
        if not path.exists():
            self.transact(lambda state: None)

    def _load(self) -> dict[str, Any]:
        if not self.path.exists():
            return self._initial()
        try:
            value = json.loads(self.path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise ReconciliationError("StateCorrupt", "state store is unreadable") from error
        if not isinstance(value, dict):
            raise ReconciliationError("StateCorrupt", "state store root is invalid")
        return value

    def _write(self, state: dict[str, Any]) -> None:
        encoded = json.dumps(state, sort_keys=True, separators=(",", ":")).encode()
        descriptor, temporary = tempfile.mkstemp(
            prefix=f".{self.path.name}.", dir=self.path.parent
        )
        try:
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(encoded)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, self.path)
            directory = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def transact(self, mutation: StateMutation) -> Any:
        self.lock_path.touch(mode=0o600, exist_ok=True)
        with self.lock_path.open("r+", encoding="utf-8") as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            state = self._load()
            result = mutation(state)
            self._write(state)
            return deepcopy(result)

    def read(self) -> dict[str, Any]:
        self.lock_path.touch(mode=0o600, exist_ok=True)
        with self.lock_path.open("r", encoding="utf-8") as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_SH)
            return deepcopy(self._load())


class HelperState:
    """Helper-owned receipts, ownership, leases, and events."""

    def __init__(self, path: Path, installation_id: str):
        self.installation_id = installation_id

        def initial() -> dict[str, Any]:
            return {
                "schema": 1,
                "installation_id": installation_id,
                "operations": {},
                "ownership": {},
                "leases": {},
                "events": [],
                "next_fence": 1,
            }

        self.store = JsonStore(path, initial)
        current = self.store.read()
        if current.get("installation_id") != installation_id:
            raise ReconciliationError("StateConflict", "installation identity changed")

    def transact(self, mutation: StateMutation) -> Any:
        return self.store.transact(mutation)

    def read(self) -> dict[str, Any]:
        return self.store.read()


class ControlPlaneState:
    """Separate unprivileged desired-state fixture."""

    def __init__(self, path: Path):
        self.store = JsonStore(path, lambda: {"schema": 1, "desired": {}})

    def set_desired(self, instance_id: str, desired: dict[str, Any]) -> None:
        self.store.transact(
            lambda state: state["desired"].__setitem__(instance_id, deepcopy(desired))
        )

    def get_desired(self, instance_id: str) -> dict[str, Any] | None:
        return self.store.read()["desired"].get(instance_id)
