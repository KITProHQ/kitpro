package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

type networkCandidateRequest struct {
	Candidate             hostadmin.NetworkCandidate `json:"candidate"`
	ConfirmationInterface string                     `json:"confirmation_interface,omitempty"`
	ExpectedDigest        string                     `json:"expected_digest,omitempty"`
}
type networkTransactionRequest struct {
	CandidateDigest       string `json:"candidate_digest,omitempty"`
	ConfirmationInterface string `json:"confirmation_interface"`
}

func decodeStrictNetworkBody(w http.ResponseWriter, r *http.Request, target any) bool {
	body := http.MaxBytesReader(w, r.Body, 16*1024)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		hostError(w, hostadmin.ErrInvalid)
		return false
	}
	return true
}

func (a *app) hostNetworkConfigurationRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	if path == "configuration/capabilities" {
		a.hostNetworkConfigurationCapabilities(w, r)
		return true
	}
	if path == "history" {
		a.hostNetworkHistory(w, r)
		return true
	}
	if strings.HasPrefix(path, "transactions/") {
		a.hostNetworkTransaction(w, r, strings.TrimPrefix(path, "transactions/"))
		return true
	}
	if !strings.HasPrefix(path, "interfaces/") {
		return false
	}
	rest := strings.TrimPrefix(path, "interfaces/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || !hostadmin.ValidInterfaceName(parts[0]) || parts[1] != "configuration" {
		return false
	}
	switch {
	case len(parts) == 2:
		a.hostNetworkConfiguration(w, r, parts[0])
	case len(parts) == 3 && parts[2] == "preview":
		a.hostNetworkConfigurationPreview(w, r, parts[0])
	case len(parts) == 3 && parts[2] == "apply":
		a.hostNetworkConfigurationApply(w, r, parts[0])
	default:
		return false
	}
	return true
}
func (a *app) networkResourceForInterface(name string) (hostadmin.NetworkPolicyResource, bool) {
	if a.hostNetworkManager == nil {
		return hostadmin.NetworkPolicyResource{}, false
	}
	for _, resource := range a.hostNetworkManager.Policy.Resources {
		if resource.Interface == name {
			return resource, true
		}
	}
	return hostadmin.NetworkPolicyResource{}, false
}
func (a *app) hostNetworkConfigurationCapabilities(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	principal, ok := a.requirePermission(w, r, auth.PermissionHostRead)
	if !ok {
		return
	}
	resources := []map[string]any{}
	if a.hostNetworkManager != nil {
		for _, resource := range a.hostNetworkManager.Policy.Resources {
			resources = append(resources, map[string]any{"id": resource.ID, "interface": resource.Interface, "backend": resource.Backend, "allowed_modes": resource.AllowedModes, "protected": resource.ManagementProtected, "qualification_only": resource.QualificationOnly})
		}
	}
	writeNetworkJSON(w, map[string]any{"enabled": a.hostNetworkManagementEnabled, "authorized": principal.Has(auth.PermissionNetworkManage), "resources": resources, "backends": map[string]string{"systemd-networkd": "transactional for exact KITPro-owned profiles", "NetworkManager": "read-only", "netplan": "read-only configuration layer"}})
}
func (a *app) hostNetworkConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	if !readOnly(w, r) {
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	resource, ok := a.networkResourceForInterface(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	backend := hostadmin.NetworkdFileBackend{Directory: env("KITPRO_NETWORKD_CONFIG_DIR", "/etc/systemd/network")}
	current, err := backend.Read(r.Context(), resource)
	if err != nil {
		hostError(w, err)
		return
	}
	writeNetworkJSON(w, hostadmin.NetworkConfiguration{ResourceID: resource.ID, Interface: resource.Interface, Backend: resource.Backend, Profile: resource.Profile, Candidate: current, Digest: hostadmin.NetworkConfigurationDigest(resource, current)})
}
func (a *app) hostNetworkConfigurationPreview(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method", 405)
		return
	}
	principal, ok := a.requirePermission(w, r, auth.PermissionHostRead)
	if !ok {
		return
	}
	var body networkCandidateRequest
	if !decodeStrictNetworkBody(w, r, &body) {
		return
	}
	resource, ok := a.networkResourceForInterface(name)
	if !ok || a.hostNetworkManager == nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preview, err := a.hostNetworkManager.Preview(ctx, resource.ID, body.Candidate)
	if err != nil {
		hostError(w, err)
		return
	}
	if preview.Eligibility.Allowed && !principal.Has(auth.PermissionNetworkManage) {
		preview.Eligibility.Allowed = false
		preview.Eligibility.State = "permission_denied"
		preview.Eligibility.Reason = "Your administrator account is not authorized for network changes."
	}
	writeNetworkJSON(w, preview)
}
func (a *app) hostNetworkConfigurationApply(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method", 405)
		return
	}
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	var body networkCandidateRequest
	if !decodeStrictNetworkBody(w, r, &body) {
		return
	}
	resource, ok := a.networkResourceForInterface(name)
	if !ok || a.hostNetworkManager == nil {
		http.NotFound(w, r)
		return
	}
	opID := operations.NewID()
	audit := networkAuditDetail{TransactionID: opID, ResourceID: resource.ID, Interface: name, Action: "apply", AuthorizationOutcome: "allowed", PolicyOutcome: "pending", State: "running"}
	actor := actorName(principal)
	deny := func(status int, reason string) {
		audit.State = "rejected"
		audit.FailureReason = reason
		if status == 403 {
			audit.AuthorizationOutcome = "denied"
		}
		if insertNetworkAudit(r.Context(), a.db, actor, audit) != nil {
			http.Error(w, "operation audit unavailable", 503)
			return
		}
		writeNetworkStatusJSON(w, status, map[string]any{"id": opID, "state": "rejected", "error": reason})
	}
	if body.ConfirmationInterface != name {
		deny(400, "The confirmation must identify the exact interface.")
		return
	}
	if !principal.Has(auth.PermissionNetworkManage) {
		deny(403, "Network-management permission is required.")
		return
	}
	if !a.hostNetworkManagementEnabled {
		deny(403, "Network configuration management is disabled.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	preview, err := a.hostNetworkManager.Preview(ctx, resource.ID, body.Candidate)
	if err != nil || !preview.Eligibility.Allowed || preview.Current.Digest != body.ExpectedDigest {
		deny(409, "The network configuration is not eligible or changed after preview.")
		return
	}
	audit.PolicyOutcome = "allowed"
	audit.PreviousDigest = preview.Current.Digest
	audit.CandidateDigest = preview.Proposed.Digest
	deadline := time.Now().UTC().Add(90 * time.Second)
	audit.ConfirmationDeadline = deadline.Format(time.RFC3339Nano)
	if insertNetworkAudit(r.Context(), a.db, actor, audit) != nil {
		http.Error(w, "operation audit unavailable", 503)
		return
	}
	if operations.Insert(r.Context(), a.db, opID, "ManageNetworkConfiguration:apply", resource.ID) != nil {
		http.Error(w, "operation unavailable", 503)
		return
	}
	if a.beforeNetworkDispatch != nil {
		a.beforeNetworkDispatch()
	}
	current, err := auth.PrincipalFor(r.Context(), a.db, principal.ID)
	if err != nil || !current.Has(auth.PermissionNetworkManage) {
		audit.State = "rejected"
		audit.AuthorizationOutcome = "denied"
		audit.FailureReason = "Network-management permission was revoked before dispatch."
		_ = updateNetworkAudit(r.Context(), a.db, audit)
		http.Error(w, audit.FailureReason, 403)
		return
	}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: opID, OperationRevision: 1, Operation: "ManageNetworkConfiguration", NetworkResourceID: resource.ID, NetworkAction: "apply", ConfirmationInterface: name, NetworkMode: body.Candidate.Mode, NetworkAddresses: body.Candidate.Addresses, NetworkGateway: body.Candidate.Gateway, NetworkDNSServers: body.Candidate.DNSServers, NetworkSearchDomains: body.Candidate.SearchDomains, ExpectedDigest: body.ExpectedDigest, CandidateDigest: preview.Proposed.Digest, Deadline: deadline.Format(time.RFC3339Nano)}
	response, callErr := a.callHelperOperation(request)
	state := "awaiting_confirmation"
	summary := "network configuration applied; confirmation required"
	status := http.StatusAccepted
	if callErr != nil || response.OperationID != opID {
		state = "outcome_unknown"
		summary = "helper connection ended before the network outcome was verified"
		status = 503
	} else if !response.OK {
		state = response.State
		if state == "" {
			state = "failed"
		}
		summary = "network configuration failed"
		status = 409
	}
	audit.State = state
	if state != "awaiting_confirmation" {
		audit.FailureReason = summary
	}
	_ = updateNetworkAudit(r.Context(), a.db, audit)
	_ = operations.Update(r.Context(), a.db, opID, state, summary)
	writeNetworkStatusJSON(w, status, map[string]any{"id": opID, "state": state, "deadline": audit.ConfirmationDeadline, "candidate_digest": audit.CandidateDigest, "result": response.Result})
}

func (a *app) loadNetworkAudit(ctx context.Context, id string) (networkAuditDetail, string, error) {
	var actor, raw string
	err := a.db.QueryRowContext(ctx, `SELECT actor,detail_json FROM audit_events WHERE event_id=? AND event_type='network-configuration-operation'`, networkAuditEventID(id)).Scan(&actor, &raw)
	if err != nil {
		return networkAuditDetail{}, "", err
	}
	var detail networkAuditDetail
	err = json.Unmarshal([]byte(raw), &detail)
	return detail, actor, err
}

// reconcileNetworkAudit reads the root-owned transaction record through the
// typed helper protocol. Independent rollback can finish while the API is
// stopped, so the API audit projection must not remain awaiting confirmation.
func (a *app) reconcileNetworkAudit(ctx context.Context, detail networkAuditDetail) networkAuditDetail {
	if detail.State != "awaiting_confirmation" && detail.State != "running" {
		return detail
	}
	request := protocol.Request{
		Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), OperationRevision: 1,
		Operation: "ManageNetworkConfiguration", NetworkResourceID: detail.ResourceID,
		NetworkTransactionID: detail.TransactionID, NetworkAction: "status", ConfirmationInterface: detail.Interface,
	}
	response, err := a.callHelperOperation(request)
	if err != nil || !response.OK {
		return detail
	}
	var tx hostadmin.NetworkTransaction
	raw, err := json.Marshal(response.Result)
	if err != nil || json.Unmarshal(raw, &tx) != nil || tx.ID != detail.TransactionID || tx.ResourceID != detail.ResourceID || tx.Interface != detail.Interface {
		return detail
	}
	if tx.State == detail.State {
		return detail
	}
	detail.State = tx.State
	detail.RollbackResult = tx.Rollback
	detail.FailureReason = tx.FailureReason
	_ = updateNetworkAudit(ctx, a.db, detail)
	return detail
}
func (a *app) hostNetworkTransaction(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	id := parts[0]
	if hostadmin.ValidateNetworkTransactionID(id) != nil {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if _, ok := a.requirePermission(w, r, auth.PermissionAuditRead); !ok {
			return
		}
		detail, actor, err := a.loadNetworkAudit(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		detail = a.reconcileNetworkAudit(r.Context(), detail)
		writeNetworkJSON(w, map[string]any{"transaction": detail, "administrator": actor})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost || (parts[1] != "confirm" && parts[1] != "rollback") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	var body networkTransactionRequest
	if !decodeStrictNetworkBody(w, r, &body) {
		return
	}
	detail, _, err := a.loadNetworkAudit(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !principal.Has(auth.PermissionNetworkManage) || body.ConfirmationInterface != detail.Interface {
		http.Error(w, "network-management authorization or exact confirmation is missing", 403)
		return
	}
	current, err := auth.PrincipalFor(r.Context(), a.db, principal.ID)
	if err != nil || !current.Has(auth.PermissionNetworkManage) {
		http.Error(w, "network-management permission was revoked", 403)
		return
	}
	helperOpID := operations.NewID()
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: helperOpID, OperationRevision: 1, Operation: "ManageNetworkConfiguration", NetworkResourceID: detail.ResourceID, NetworkTransactionID: id, NetworkAction: parts[1], ConfirmationInterface: detail.Interface, CandidateDigest: body.CandidateDigest}
	response, callErr := a.callHelperOperation(request)
	if callErr != nil || !response.OK {
		detail.State = "outcome_unknown"
		detail.FailureReason = "network transaction result could not be verified"
		_ = updateNetworkAudit(r.Context(), a.db, detail)
		writeNetworkStatusJSON(w, http.StatusConflict, map[string]any{"id": id, "state": detail.State, "result": response.Result})
		return
	}
	var tx hostadmin.NetworkTransaction
	raw, _ := json.Marshal(response.Result)
	_ = json.Unmarshal(raw, &tx)
	detail.State = tx.State
	detail.RollbackResult = tx.Rollback
	detail.FailureReason = tx.FailureReason
	_ = updateNetworkAudit(r.Context(), a.db, detail)
	lifecycle := detail
	lifecycle.TransactionID = helperOpID
	lifecycle.CorrelationID = id
	lifecycle.Action = parts[1]
	_ = insertNetworkAudit(r.Context(), a.db, actorName(principal), lifecycle)
	writeNetworkJSON(w, map[string]any{"id": id, "state": tx.State, "result": tx})
}
