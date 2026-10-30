"""Domain values for the disposable reconciliation fixture."""

from __future__ import annotations

import hashlib
import json
import re
import uuid
from datetime import datetime, timezone
from typing import Any


ID_RE = re.compile(r"^[a-z][a-z0-9-]{0,62}$")
TEST_LABEL_PREFIX = "invalid.kitpro.reconciliation-test"


class ReconciliationError(Exception):
    """Typed fixture failure."""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


class SimulatedCrash(Exception):
    """Test-only abrupt helper failure."""


class DockerFailure(Exception):
    """Docker proved that a mutation failed before it changed state."""


class OutcomeUnknown(Exception):
    """The Docker connection failed without proving the mutation outcome."""


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def canonical_hash(value: dict[str, Any]) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def validate_stable_id(value: str, field: str) -> None:
    if not ID_RE.fullmatch(value):
        raise ReconciliationError("InvalidRequest", f"{field} is invalid")


def validate_operation_id(value: str) -> None:
    try:
        parsed = uuid.UUID(value)
    except (AttributeError, TypeError, ValueError) as error:
        raise ReconciliationError("InvalidRequest", "operation_id is invalid") from error
    if str(parsed) != value:
        raise ReconciliationError("InvalidRequest", "operation_id is not canonical")


def container_name(installation_id: str, instance_id: str) -> str:
    suffix = hashlib.sha256(f"{installation_id}:{instance_id}".encode()).hexdigest()[:16]
    return f"kitpro-reconcile-test-{suffix}"


def expected_labels(installation_id: str, instance_id: str) -> dict[str, str]:
    return {
        f"{TEST_LABEL_PREFIX}.managed": "true",
        f"{TEST_LABEL_PREFIX}.installation": installation_id,
        f"{TEST_LABEL_PREFIX}.instance": instance_id,
        f"{TEST_LABEL_PREFIX}.resource": "container",
    }


def expected_config(
    installation_id: str, instance_id: str, image_digest: str
) -> dict[str, Any]:
    return {
        "name": container_name(installation_id, instance_id),
        "image_digest": image_digest,
        "labels": expected_labels(installation_id, instance_id),
        "networks": [f"kitpro-reconcile-{instance_id}"],
        "privileged": False,
    }
