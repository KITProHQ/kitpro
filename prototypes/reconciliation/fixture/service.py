"""State machine for the disposable reconciliation fixture."""

from __future__ import annotations

from copy import deepcopy
from typing import Any

from .fake_docker import FakeDocker
from .model import (
    DockerFailure,
    OutcomeUnknown,
    ReconciliationError,
    SimulatedCrash,
    canonical_hash,
    expected_config,
    now,
    validate_operation_id,
    validate_stable_id,
)
from .store import HelperState


class ReconciliationService:
    def __init__(self, state: HelperState, docker: FakeDocker):
        self.state = state
        self.docker = docker

    def install(
        self,
        operation_id: str,
        instance_id: str,
        image_digest: str,
        *,
        fault: str | None = None,
    ) -> dict[str, Any]:
        validate_operation_id(operation_id)
        validate_stable_id(instance_id, "instance_id")
        if not image_digest.startswith("sha256:") or len(image_digest) != 71:
            raise ReconciliationError("InvalidRequest", "image digest is invalid")

        request = {
            "operation": "InstallTestInstance",
            "instance_id": instance_id,
            "image_digest": image_digest,
        }
        request_hash = canonical_hash(request)
        config = expected_config(self.state.installation_id, instance_id, image_digest)
        config_hash = canonical_hash(config)

        def accept(data: dict[str, Any]) -> dict[str, Any]:
            existing = data["operations"].get(operation_id)
            if existing is not None:
                if existing["request_hash"] != request_hash:
                    raise ReconciliationError(
                        "OperationConflict", "operation ID is bound to another request"
                    )
                return {"receipt": existing, "new": False}
            lease = data["leases"].get(instance_id)
            if lease is not None:
                raise ReconciliationError("InstanceBusy", "instance mutation is leased")
            fence = data["next_fence"]
            data["next_fence"] += 1
            timestamp = now()
            receipt = {
                "operation_id": operation_id,
                "operation_type": "InstallTestInstance",
                "instance_id": instance_id,
                "caller_identity": "kitpro-api-test",
                "request_hash": request_hash,
                "created_at": timestamp,
                "started_at": timestamp,
                "completed_at": None,
                "updated_at": timestamp,
                "state": "accepted",
                "phase": "create_container",
                "phase_attempt": 1,
                "outcome": "not_dispatched",
                "recovery": "reconcile_first",
                "result": None,
                "failure": None,
                "resources_created": [],
                "resources_observed": [],
                "rollback": None,
                "audit_event_ids": [],
                "fence": fence,
            }
            owner = {
                "instance_id": instance_id,
                "resource_kind": "container",
                "status": "prepared",
                "expected_name": config["name"],
                "expected_config_hash": config_hash,
                "expected_config": config,
                "object_id": None,
                "created_by_operation": operation_id,
            }
            data["operations"][operation_id] = receipt
            data["ownership"][instance_id] = owner
            data["leases"][instance_id] = {
                "operation_id": operation_id,
                "fence": fence,
                "acquired_at": timestamp,
                "updated_at": timestamp,
            }
            self._event(data, receipt, "operation_accepted")
            return {"receipt": receipt, "new": True}

        accepted = self.state.transact(accept)
        if not accepted["new"]:
            return accepted["receipt"]

        if fault == "crash_before_docker":
            raise SimulatedCrash("helper stopped before Docker mutation")

        self._mark_dispatching(operation_id)
        try:
            resource = self.docker.create_container(config)
        except DockerFailure as error:
            return self._finish_failure(
                operation_id,
                "DockerRejected",
                str(error),
                outcome="confirmed_not_applied",
                recovery="retry_after_validation",
            )
        except OutcomeUnknown as error:
            return self._mark_uncertain(operation_id, str(error))

        if fault == "crash_after_docker":
            raise SimulatedCrash("helper stopped after Docker mutation")
        return self._finish_success(operation_id, resource)

    def reconcile_operation(self, operation_id: str) -> dict[str, Any]:
        receipt = self.get_operation(operation_id)
        if receipt["state"] in {"succeeded", "cancelled", "superseded"}:
            return receipt
        owner = self.state.read()["ownership"].get(receipt["instance_id"])
        if owner is None or owner.get("created_by_operation") != operation_id:
            return self._finish_action_required(
                operation_id, "OwnershipConflict", "prepared ownership intent is missing"
            )
        observed = self.docker.inspect_name(owner["expected_name"])
        if observed is None:
            return self._finish_failure(
                operation_id,
                "ConfirmedAbsent",
                "deterministic resource is absent",
                outcome="confirmed_not_applied",
                recovery="retry_after_validation",
            )
        drift = self._compare(owner, observed)
        if drift is not None:
            return self._finish_action_required(operation_id, drift, "resource differs")
        return self._finish_success(operation_id, observed)

    def startup_reconcile(self) -> list[dict[str, Any]]:
        data = self.state.read()
        pending = [
            operation_id
            for operation_id, receipt in data["operations"].items()
            if receipt["state"] in {"accepted", "executing", "reconciling"}
        ]
        return [self.reconcile_operation(operation_id) for operation_id in pending]

    def inspect_instance(self, instance_id: str) -> dict[str, Any]:
        validate_stable_id(instance_id, "instance_id")
        data = self.state.read()
        owner = data["ownership"].get(instance_id)
        expected = owner["expected_name"] if owner is not None else None
        if expected is None:
            # Deterministic discovery is a hint only. Search the fake adapter by
            # the name KITPro would have prepared for this instance.
            probe = expected_config(
                self.state.installation_id, instance_id, "sha256:" + "0" * 64
            )["name"]
            observed = self.docker.inspect_name(probe)
            if observed is not None:
                return self._drift("OwnershipConflict", observed, False)
            return self._drift("MissingResource", None, False)
        observed = self.docker.inspect_name(expected)
        if observed is None:
            return self._drift("MissingResource", None, False)
        drift = self._compare(owner, observed)
        if drift is not None:
            return self._drift(drift, observed, False)
        return self._drift("Healthy", observed, True)

    def mark_superseded(self, operation_id: str, administrator: str) -> dict[str, Any]:
        if not administrator:
            raise ReconciliationError("InvalidRequest", "administrator is required")

        def change(data: dict[str, Any]) -> dict[str, Any]:
            receipt = self._receipt(data, operation_id)
            if receipt["state"] in {"executing", "reconciling", "succeeded"}:
                raise ReconciliationError(
                    "InvalidTransition", "active or successful operation cannot be superseded"
                )
            receipt["state"] = "superseded"
            receipt["recovery"] = "completed"
            receipt["completed_at"] = now()
            receipt["updated_at"] = receipt["completed_at"]
            data["leases"].pop(receipt["instance_id"], None)
            self._event(
                data,
                receipt,
                "operation_superseded",
                {"administrator": administrator},
            )
            return receipt

        return self.state.transact(change)

    def get_operation(self, operation_id: str) -> dict[str, Any]:
        data = self.state.read()
        receipt = data["operations"].get(operation_id)
        if receipt is None:
            raise ReconciliationError("NotFound", "operation does not exist")
        return receipt

    def _mark_dispatching(self, operation_id: str) -> None:
        def change(data: dict[str, Any]) -> None:
            receipt = self._receipt(data, operation_id)
            self._check_fence(data, receipt)
            receipt["state"] = "executing"
            receipt["outcome"] = "unknown"
            receipt["updated_at"] = now()
            self._event(data, receipt, "phase_dispatching")

        self.state.transact(change)

    def _mark_uncertain(self, operation_id: str, detail: str) -> dict[str, Any]:
        def change(data: dict[str, Any]) -> dict[str, Any]:
            receipt = self._receipt(data, operation_id)
            self._check_fence(data, receipt)
            receipt["state"] = "reconciling"
            receipt["outcome"] = "unknown"
            receipt["recovery"] = "reconcile_first"
            receipt["failure"] = {"class": "OutcomeUnknown", "detail": detail}
            receipt["updated_at"] = now()
            self._event(data, receipt, "external_outcome_unknown")
            return receipt

        return self.state.transact(change)

    def _finish_success(
        self, operation_id: str, observed: dict[str, Any]
    ) -> dict[str, Any]:
        def change(data: dict[str, Any]) -> dict[str, Any]:
            receipt = self._receipt(data, operation_id)
            self._check_fence(data, receipt)
            owner = data["ownership"].get(receipt["instance_id"])
            if owner is None or self._compare(owner, observed) is not None:
                raise ReconciliationError(
                    "OwnershipConflict", "observed resource does not match intent"
                )
            owner["status"] = "confirmed"
            owner["object_id"] = observed["object_id"]
            receipt["state"] = "succeeded"
            receipt["outcome"] = "confirmed_applied"
            receipt["recovery"] = "completed"
            receipt["result"] = "installed"
            receipt["resources_created"] = [observed["object_id"]]
            receipt["resources_observed"] = [observed["object_id"]]
            receipt["completed_at"] = now()
            receipt["updated_at"] = receipt["completed_at"]
            data["leases"].pop(receipt["instance_id"], None)
            self._event(data, receipt, "operation_succeeded")
            return receipt

        return self.state.transact(change)

    def _finish_failure(
        self,
        operation_id: str,
        failure_class: str,
        detail: str,
        *,
        outcome: str,
        recovery: str,
    ) -> dict[str, Any]:
        def change(data: dict[str, Any]) -> dict[str, Any]:
            receipt = self._receipt(data, operation_id)
            self._check_fence(data, receipt)
            receipt["state"] = "failed"
            receipt["outcome"] = outcome
            receipt["recovery"] = recovery
            receipt["failure"] = {"class": failure_class, "detail": detail}
            receipt["completed_at"] = now()
            receipt["updated_at"] = receipt["completed_at"]
            data["leases"].pop(receipt["instance_id"], None)
            self._event(data, receipt, "operation_failed")
            return receipt

        return self.state.transact(change)

    def _finish_action_required(
        self, operation_id: str, failure_class: str, detail: str
    ) -> dict[str, Any]:
        def change(data: dict[str, Any]) -> dict[str, Any]:
            receipt = self._receipt(data, operation_id)
            self._check_fence(data, receipt)
            receipt["state"] = "action_required"
            receipt["outcome"] = "unknown"
            receipt["recovery"] = "administrator_action"
            receipt["failure"] = {"class": failure_class, "detail": detail}
            receipt["updated_at"] = now()
            data["leases"].pop(receipt["instance_id"], None)
            self._event(data, receipt, "administrator_action_required")
            return receipt

        return self.state.transact(change)

    @staticmethod
    def _compare(owner: dict[str, Any], observed: dict[str, Any]) -> str | None:
        expected = owner["expected_config"]
        if observed.get("privileged") is not False or set(observed.get("networks", [])) != set(
            expected["networks"]
        ):
            return "SecurityDrift"
        if owner.get("object_id") not in {None, observed.get("object_id")}:
            return "OwnershipConflict"
        if observed.get("name") != expected["name"]:
            return "OwnershipConflict"
        if observed.get("labels") != expected["labels"]:
            return "OwnershipConflict"
        if observed.get("image_digest") != expected["image_digest"]:
            return "UserModification"
        candidate = {key: observed.get(key) for key in expected}
        if canonical_hash(candidate) != owner["expected_config_hash"]:
            return "UserModification"
        return None

    @staticmethod
    def _drift(
        classification: str, observed: dict[str, Any] | None, owned: bool
    ) -> dict[str, Any]:
        return {
            "classification": classification,
            "ownership_proven": owned and classification == "Healthy",
            "automatic_mutation_allowed": False,
            "observed_object_id": observed.get("object_id") if observed else None,
        }

    @staticmethod
    def _receipt(data: dict[str, Any], operation_id: str) -> dict[str, Any]:
        receipt = data["operations"].get(operation_id)
        if receipt is None:
            raise ReconciliationError("NotFound", "operation does not exist")
        return receipt

    @staticmethod
    def _check_fence(data: dict[str, Any], receipt: dict[str, Any]) -> None:
        lease = data["leases"].get(receipt["instance_id"])
        if lease is None or lease["operation_id"] != receipt["operation_id"]:
            raise ReconciliationError("StaleExecutor", "operation no longer owns lease")
        if lease["fence"] != receipt["fence"]:
            raise ReconciliationError("StaleExecutor", "fencing token is stale")

    @staticmethod
    def _event(
        data: dict[str, Any],
        receipt: dict[str, Any],
        kind: str,
        facts: dict[str, Any] | None = None,
    ) -> None:
        event_id = f"event-{len(data['events']) + 1}"
        event = {
            "event_id": event_id,
            "timestamp": now(),
            "kind": kind,
            "operation_id": receipt["operation_id"],
            "instance_id": receipt["instance_id"],
            "facts": deepcopy(facts or {}),
        }
        data["events"].append(event)
        receipt["audit_event_ids"].append(event_id)
