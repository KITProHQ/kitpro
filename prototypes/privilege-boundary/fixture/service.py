"""Policy and ownership checks for the disposable test helper."""

from __future__ import annotations

from dataclasses import asdict
from threading import RLock
from typing import Any, Callable, Protocol

from .docker_api import DockerObject
from .protocol import MUTATING_OPERATIONS, ProtocolError, Request
from .safe_fs import TestStorage
from .state import StateStore


TEST_LABEL_NAMESPACE = "invalid.kitpro.privilege-boundary-test"


class DockerOperations(Protocol):
    def inspect_runtime(self) -> dict[str, Any]: ...
    def list_containers(self, labels: dict[str, str]) -> list[DockerObject]: ...
    def list_network_members(self, network_name: str) -> tuple[str, ...]: ...
    def list_networks(self, labels: dict[str, str]) -> list[DockerObject]: ...
    def create_network(self, name: str, labels: dict[str, str]) -> DockerObject: ...
    def inspect_network(self, object_id: str) -> DockerObject: ...
    def remove_network(self, object_id: str) -> None: ...
    def create_container(
        self, name: str, image: str, network_name: str, labels: dict[str, str]
    ) -> DockerObject: ...
    def inspect_container(self, object_id: str) -> DockerObject: ...
    def start_container(self, object_id: str) -> None: ...
    def stop_container(self, object_id: str) -> None: ...
    def remove_container(self, object_id: str) -> None: ...


Audit = Callable[[dict[str, Any]], None]


class BoundaryService:
    """Expose semantic operations, not Docker or root primitives."""

    def __init__(
        self,
        *,
        docker: DockerOperations,
        state: StateStore,
        storage: TestStorage,
        image_digest: str,
        run_id: str,
        audit: Audit,
    ):
        self.docker = docker
        self.state = state
        self.storage = storage
        self.image_digest = image_digest
        self.run_id = run_id
        self.audit = audit
        # The fixture uses one conservative mutation lock. Production can refine
        # this to instance and shared-resource locks without changing the wire API.
        self._mutation_lock = RLock()

    def execute(self, request: Request, peer_uid: int) -> dict[str, Any]:
        if request.operation_kind == "GetOperation":
            return self.state.get_operation(request.parameters["subject_operation_id"])

        cached = self.state.begin(request, peer_uid)
        if cached is not None:
            self.audit(
                {
                    "event": "operation_replayed",
                    "operation_id": request.operation_id,
                    "operation_kind": request.operation_kind,
                    "peer_uid": peer_uid,
                    "status": cached["status"],
                }
            )
            if cached["status"] == "completed":
                return cached["result"]
            error = cached["error"]
            raise ProtocolError(
                str(error["code"]),
                str(error["message"]),
                retryable=bool(error.get("retryable", False)),
            )

        self.audit(
            {
                "event": "operation_started",
                "operation_id": request.operation_id,
                "operation_kind": request.operation_kind,
                "peer_uid": peer_uid,
            }
        )
        try:
            if request.operation_kind in MUTATING_OPERATIONS:
                with self._mutation_lock:
                    result = self._dispatch(request, peer_uid)
            else:
                result = self._dispatch(request, peer_uid)
        except ProtocolError as error:
            safe_error = {
                "code": error.code,
                "message": error.message,
                "retryable": error.retryable,
            }
            self.state.finish(request, error=safe_error)
            self.audit(
                {
                    "event": "operation_failed",
                    "operation_id": request.operation_id,
                    "operation_kind": request.operation_kind,
                    "peer_uid": peer_uid,
                    "error_code": error.code,
                }
            )
            raise
        self.state.finish(request, result=result)
        self.audit(
            {
                "event": "operation_completed",
                "operation_id": request.operation_id,
                "operation_kind": request.operation_kind,
                "peer_uid": peer_uid,
            }
        )
        return result

    def _dispatch(self, request: Request, peer_uid: int) -> dict[str, Any]:
        operation = request.operation_kind
        if operation == "Ping":
            return {
                "message": "pong",
                "authenticated_peer_uid": peer_uid,
                "human_authorization_verified": False,
                "fixture": "disposable-non-production",
            }
        if operation == "InspectRuntime":
            return self.docker.inspect_runtime()
        if operation == "PrepareTestDirectory":
            return asdict(self.storage.prepare(request.parameters["slot_id"]))

        instance_id = request.parameters["instance_id"]
        if operation == "CreateTestContainer":
            return self._create(instance_id)
        if operation == "InspectTestContainer":
            return self._inspect_result(instance_id)
        if operation == "StartTestContainer":
            container, _network = self._owned_pair(instance_id)
            self.docker.start_container(container.object_id)
            return self._inspect_result(instance_id)
        if operation == "StopTestContainer":
            container, _network = self._owned_pair(instance_id)
            self.docker.stop_container(container.object_id)
            return self._inspect_result(instance_id)
        if operation == "RemoveTestContainer":
            return self._remove(instance_id)
        raise ProtocolError("UnsupportedOperation", "unsupported operation")

    def _labels(self, instance_id: str, resource_type: str) -> dict[str, str]:
        prefix = TEST_LABEL_NAMESPACE
        return {
            f"{prefix}.managed": "true",
            f"{prefix}.fixture-run": self.run_id,
            f"{prefix}.instance": instance_id,
            f"{prefix}.resource-type": resource_type,
            f"{prefix}.disposable": "true",
        }

    def _name(self, instance_id: str, resource_type: str) -> str:
        suffix = "ctr" if resource_type == "container" else "net"
        return f"kitpro-pb-test-{self.run_id[:8]}-{instance_id}-{suffix}"

    def _record(self, resource: DockerObject, labels: dict[str, str]) -> dict[str, Any]:
        return {
            "kind": "container" if labels[f"{TEST_LABEL_NAMESPACE}.resource-type"] == "container" else "network",
            "docker_id": resource.object_id,
            "name": resource.name,
            "expected_labels": labels,
        }

    def _create(self, instance_id: str) -> dict[str, Any]:
        container_record = self.state.get_resource(instance_id, "container")
        network_record = self.state.get_resource(instance_id, "network")
        if container_record is not None or network_record is not None:
            if container_record is None or network_record is None:
                raise ProtocolError(
                    "OwnershipConflict", "instance has an incomplete ownership record"
                )
            return self._inspect_result(instance_id)

        container_labels = self._labels(instance_id, "container")
        network_labels = self._labels(instance_id, "network")
        if self.docker.list_containers(container_labels):
            raise ProtocolError(
                "OwnershipConflict", "label-only container requires manual recovery"
            )
        if self.docker.list_networks(network_labels):
            raise ProtocolError(
                "OwnershipConflict", "label-only network requires manual recovery"
            )

        network = self.docker.create_network(
            self._name(instance_id, "network"), network_labels
        )
        self.state.put_resource(
            instance_id, "network", self._record(network, network_labels)
        )
        try:
            container = self.docker.create_container(
                self._name(instance_id, "container"),
                self.image_digest,
                network.name,
                container_labels,
            )
        except ProtocolError:
            # Keep both the Docker network and its record for explicit recovery.
            raise
        container_record = self._record(container, container_labels)
        container_record["image_digest"] = self.image_digest
        self.state.put_resource(instance_id, "container", container_record)
        return self._inspect_result(instance_id)

    def _owned(self, instance_id: str, kind: str) -> DockerObject:
        record = self.state.get_resource(instance_id, kind)
        if record is None:
            raise ProtocolError("OwnershipUnproven", "helper ownership record is missing")
        if record.get("kind") != kind:
            raise ProtocolError("OwnershipUnproven", "resource type does not match record")
        object_id = record.get("docker_id")
        expected_name = record.get("name")
        expected_labels = record.get("expected_labels")
        if not isinstance(object_id, str) or not object_id:
            raise ProtocolError("OwnershipUnproven", "record has no Docker object ID")
        if not isinstance(expected_name, str) or not isinstance(expected_labels, dict):
            raise ProtocolError("OwnershipUnproven", "ownership record is malformed")
        try:
            resource = (
                self.docker.inspect_container(object_id)
                if kind == "container"
                else self.docker.inspect_network(object_id)
            )
        except ProtocolError as error:
            if error.code == "NotFound":
                raise ProtocolError(
                    "OwnershipUnproven", "recorded Docker resource is missing"
                ) from error
            raise
        if resource.name != expected_name:
            raise ProtocolError("OwnershipUnproven", "resource name does not match record")
        if resource.labels != expected_labels:
            raise ProtocolError("OwnershipUnproven", "resource labels do not match record")
        if expected_labels != self._labels(instance_id, kind):
            raise ProtocolError("OwnershipUnproven", "recorded ownership scope is invalid")
        if kind == "container":
            if record.get("image_digest") != self.image_digest:
                raise ProtocolError("OwnershipUnproven", "recorded image identity changed")
            if resource.image != self.image_digest:
                raise ProtocolError("OwnershipUnproven", "container image identity changed")
            network = self._owned(instance_id, "network")
            if resource.networks != (network.name,):
                raise ProtocolError(
                    "PolicyDenied", "container network attachment is outside policy"
                )
        return resource

    def _inspect_result(self, instance_id: str) -> dict[str, Any]:
        container, network = self._owned_pair(instance_id)
        return {
            "instance_id": instance_id,
            "container": {
                "name": container.name,
                "state": container.state,
                "image_digest": container.image,
            },
            "network": {"name": network.name, "internal": True},
            "published_ports": [],
        }

    def _remove(self, instance_id: str) -> dict[str, Any]:
        # Prove ownership of every target before the first destructive call.
        container, network = self._owned_pair(instance_id)
        self.docker.remove_container(container.object_id)
        self.state.remove_resource(instance_id, "container")
        self.docker.remove_network(network.object_id)
        self.state.remove_resource(instance_id, "network")
        return {"instance_id": instance_id, "removed": True}

    def _owned_pair(self, instance_id: str) -> tuple[DockerObject, DockerObject]:
        container = self._owned(instance_id, "container")
        network = self._owned(instance_id, "network")
        # Docker 29's network-inspect member map omits created and stopped
        # endpoints. Query all containers by network instead so a stopped
        # foreign endpoint cannot hide from destructive policy checks.
        members = self.docker.list_network_members(network.name)
        if members != (container.object_id,):
            raise ProtocolError(
                "PolicyDenied", "application network membership is outside policy"
            )
        return container, network
