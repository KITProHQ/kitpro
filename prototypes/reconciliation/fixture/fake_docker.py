"""Small fake Docker boundary for reconciliation semantics."""

from __future__ import annotations

import threading
import uuid
from copy import deepcopy
from typing import Any

from .model import DockerFailure, OutcomeUnknown, ReconciliationError


class FakeDocker:
    def __init__(self):
        self._lock = threading.Lock()
        self.resources: dict[str, dict[str, Any]] = {}
        self.create_calls = 0
        self.mode = "success"
        self.pause_entered: threading.Event | None = None
        self.pause_release: threading.Event | None = None

    def create_container(self, config: dict[str, Any]) -> dict[str, Any]:
        entered = self.pause_entered
        release = self.pause_release
        if entered is not None and release is not None:
            entered.set()
            if not release.wait(timeout=5):
                raise DockerFailure("test pause timed out")

        with self._lock:
            self.create_calls += 1
            if self.mode == "fail_before":
                raise DockerFailure("Docker rejected the request before mutation")
            if self.mode == "unknown_before":
                raise OutcomeUnknown("Docker outcome is unknown")
            name = config["name"]
            if name in self.resources:
                raise ReconciliationError("DockerConflict", "deterministic name exists")
            resource = deepcopy(config)
            resource["object_id"] = uuid.uuid4().hex
            resource["state"] = "running"
            self.resources[name] = resource
            if self.mode == "unknown_after":
                raise OutcomeUnknown("Docker applied the request before disconnect")
            return deepcopy(resource)

    def inspect_name(self, name: str) -> dict[str, Any] | None:
        with self._lock:
            value = self.resources.get(name)
            return deepcopy(value) if value is not None else None

    def remove_name(self, name: str) -> None:
        with self._lock:
            self.resources.pop(name, None)

    def replace(self, name: str, changes: dict[str, Any]) -> dict[str, Any]:
        with self._lock:
            current = deepcopy(self.resources[name])
            current.update(deepcopy(changes))
            current["object_id"] = uuid.uuid4().hex
            self.resources[name] = current
            return deepcopy(current)

    def create_unrecorded(self, config: dict[str, Any]) -> dict[str, Any]:
        with self._lock:
            resource = deepcopy(config)
            resource["object_id"] = uuid.uuid4().hex
            resource["state"] = "running"
            self.resources[config["name"]] = resource
            return deepcopy(resource)
