package main

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func networkTransactionController() (*hostadmin.NetworkTransactionController, error) {
	policy, err := hostadmin.LoadNetworkPolicy(env("KITPRO_HOST_NETWORK_POLICY", "/etc/kitpro/network-management.json"))
	if err != nil {
		return nil, err
	}
	backend := hostadmin.NetworkdFileBackend{Directory: env("KITPRO_NETWORKD_CONFIG_DIR", "/etc/systemd/network"), Reload: hostadmin.NetworkdReload, Reconfigure: hostadmin.NetworkdReconfigure, ObserveAddresses: hostadmin.ObserveInterfaceAddresses}
	scheduler := hostadmin.NewSystemdNetworkRecoveryScheduler()
	controller := hostadmin.NewNetworkTransactionController(policy, backend, scheduler)
	controller.SafetyCheck = func(ctx context.Context, resource hostadmin.NetworkPolicyResource) error {
		return hostadmin.VerifyNetworkResourceSafeKernel(resource)
	}
	return controller, nil
}

func networkCandidateFromRequest(r protocol.Request) hostadmin.NetworkCandidate {
	return hostadmin.NetworkCandidate{Mode: r.NetworkMode, Addresses: append([]string(nil), r.NetworkAddresses...), Gateway: r.NetworkGateway, DNSServers: append([]string(nil), r.NetworkDNSServers...), SearchDomains: append([]string(nil), r.NetworkSearchDomains...)}
}

func manageNetworkConfiguration(w io.Writer, request protocol.Request) {
	targetID := request.NetworkTransactionID
	if request.NetworkAction == "apply" {
		targetID = request.OperationID
	}
	if !hostadmin.ValidNetworkResourceID(request.NetworkResourceID) || hostadmin.ValidateNetworkTransactionID(targetID) != nil || request.ConfirmationInterface == "" {
		protocol.Write(w, protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: "rejected", ErrorCode: "InvalidRequest", Error: "invalid network transaction request"})
		return
	}
	if env("KITPRO_HOST_NETWORK_MANAGEMENT_ENABLED", "") != "1" {
		protocol.Write(w, protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: "rejected", ErrorCode: "NetworkActionDenied", Error: "network configuration management is disabled"})
		return
	}
	controller, err := networkTransactionController()
	if err != nil {
		protocol.Write(w, protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: "rejected", ErrorCode: "NetworkPolicyUnavailable", Error: "trusted network policy unavailable"})
		return
	}
	resource, ok := controller.Policy.Resource(request.NetworkResourceID)
	if !ok || resource.Interface != request.ConfirmationInterface {
		protocol.Write(w, protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: "rejected", ErrorCode: "NetworkActionDenied", Error: "network resource is not approved"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tx hostadmin.NetworkTransaction
	switch request.NetworkAction {
	case "apply":
		deadline, parseErr := hostadmin.ParseNetworkDeadline(request.Deadline, time.Now().UTC())
		if parseErr != nil {
			err = parseErr
			break
		}
		tx, err = controller.Apply(ctx, targetID, request.NetworkResourceID, request.ExpectedDigest, networkCandidateFromRequest(request), deadline)
	case "confirm":
		tx, err = controller.Confirm(ctx, targetID, request.CandidateDigest)
	case "rollback":
		tx, err = controller.Rollback(ctx, targetID, "administrator requested rollback")
	case "status":
		tx, err = controller.Get(targetID)
	default:
		err = hostadmin.ErrInvalid
	}
	if err != nil {
		slog.Default().Error("network configuration operation failed", "event", "network_configuration_operation_failed", "operation_id", request.OperationID, "resource", request.NetworkResourceID, "action", request.NetworkAction, "error", err)
		state := "failed"
		code := "NetworkActionFailed"
		if tx.State != "" {
			state = tx.State
		}
		if state == "outcome_unknown" || state == "rollback_failed" {
			code = "NetworkActionOutcomeUnknown"
		}
		protocol.Write(w, protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: state, ErrorCode: code, Error: "network transaction did not complete", Result: tx})
		return
	}
	protocol.Write(w, protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: tx.State, Result: tx})
}

func recoverNetworkTransaction(id string) error {
	controller, err := networkTransactionController()
	if err != nil {
		return err
	}
	_, err = controller.Rollback(context.Background(), id, "confirmation deadline expired")
	return err
}

func recoverNetworkTransactionsAtStartup() (int, error) {
	if env("KITPRO_HOST_NETWORK_MANAGEMENT_ENABLED", "") != "1" {
		return 0, nil
	}
	controller, err := networkTransactionController()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return controller.RecoverPending(ctx)
}
