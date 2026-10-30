package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func (a *app) hostStorage(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	principal, ok := a.requirePermission(w, r, auth.PermissionHostRead)
	if !ok {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	inventory, err := a.hostStorageProvider.Collect(ctx)
	if err != nil {
		hostError(w, err)
		return
	}
	if a.hostStorageManagementEnabled && a.hostStorageManager != nil {
		for _, resource := range a.hostStorageManager.Resources() {
			for _, action := range resource.Actions {
				preview, previewErr := a.hostStorageManager.Preview(ctx, resource.ID, action)
				if previewErr == nil {
					if preview.Eligibility.Allowed && !principal.Has(auth.PermissionStorageManage) {
						preview.Eligibility.Allowed = false
						preview.Eligibility.State = "permission_denied"
						preview.Eligibility.Reason = "Your administrator account is not authorized for storage operations."
						preview.Eligibility.Risk = "denied"
					}
					inventory.Management = append(inventory.Management, preview)
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(inventory)
}

type storageActionRequest struct {
	Action               hostadmin.StorageAction `json:"action"`
	ConfirmationResource string                  `json:"confirmation_resource"`
}

func (a *app) hostStorageResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/host/storage/"), "/")
	if strings.HasSuffix(path, "/preview") {
		a.hostStoragePreview(w, r, strings.TrimSuffix(path, "/preview"))
		return
	}
	if strings.HasSuffix(path, "/actions") {
		a.hostStorageAction(w, r, strings.TrimSuffix(path, "/actions"))
		return
	}
	if strings.HasSuffix(path, "/history") {
		a.hostStorageHistory(w, r, strings.TrimSuffix(path, "/history"))
		return
	}
	hostError(w, hostadmin.ErrInvalid)
}

func (a *app) hostStoragePreview(w http.ResponseWriter, r *http.Request, path string) {
	if !readOnly(w, r) || !validQuery(r) {
		if r.Method == http.MethodGet {
			hostError(w, hostadmin.ErrInvalid)
		}
		return
	}
	principal, ok := a.requirePermission(w, r, auth.PermissionHostRead)
	if !ok {
		return
	}
	parts := strings.Split(path, "/actions/")
	if len(parts) != 2 || !hostadmin.ValidStorageResourceID(parts[0]) || !hostadmin.ValidStorageAction(hostadmin.StorageAction(parts[1])) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if !a.hostStorageManagementEnabled || a.hostStorageManager == nil {
		http.Error(w, "storage management unavailable", http.StatusNotImplemented)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	preview, err := a.hostStorageManager.Preview(ctx, parts[0], hostadmin.StorageAction(parts[1]))
	if err != nil {
		hostError(w, err)
		return
	}
	if preview.Eligibility.Allowed && !principal.Has(auth.PermissionStorageManage) {
		preview.Eligibility.Allowed = false
		preview.Eligibility.State = "permission_denied"
		preview.Eligibility.Reason = "Your administrator account is not authorized for storage operations."
		preview.Eligibility.Risk = "denied"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(preview)
}

func (a *app) hostStorageAction(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !validQuery(r) || !hostadmin.ValidStorageResourceID(id) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body storageActionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !hostadmin.ValidStorageAction(body.Action) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	opID := operations.NewID()
	if operations.Insert(r.Context(), a.db, opID, "ManageStorageMount:"+string(body.Action), id) != nil {
		http.Error(w, "operation unavailable", http.StatusServiceUnavailable)
		return
	}
	actor := principal.Username
	if actor == "" {
		actor = fmt.Sprintf("administrator-%d", principal.ID)
	}
	audit := storageAuditDetail{OperationID: opID, ResourceID: id, Action: string(body.Action), AuthorizationOutcome: "allowed", PolicyOutcome: "pending", Status: "running"}
	inserted := false
	deny := func(status int, code, reason string) {
		audit.Status = "rejected"
		audit.FailureReason = reason
		if code == "permission_denied" {
			audit.AuthorizationOutcome = "denied"
		} else {
			audit.PolicyOutcome = "denied"
		}
		if !inserted {
			if insertStorageAudit(r.Context(), a.db, actor, audit) != nil {
				_ = operations.Update(r.Context(), a.db, opID, "failed", "audit persistence unavailable")
				http.Error(w, "operation audit unavailable", 503)
				return
			}
			inserted = true
		}
		_ = operations.Update(r.Context(), a.db, opID, "rejected", reason)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": opID, "status": "rejected", "error_code": code, "error": reason})
	}
	if body.ConfirmationResource != id {
		deny(400, "confirmation_mismatch", "The confirmation must identify the exact storage resource.")
		return
	}
	if !principal.Has(auth.PermissionStorageManage) {
		deny(403, "permission_denied", "Your administrator account is not authorized for storage operations.")
		return
	}
	if !a.hostStorageManagementEnabled || a.hostStorageManager == nil {
		deny(403, "permission_denied", "Storage management is disabled or has no trusted policy.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	preview, err := a.hostStorageManager.Preview(ctx, id, body.Action)
	if err != nil {
		deny(hostErrorStatus(err), "storage_unavailable", "The storage resource could not be evaluated safely.")
		return
	}
	audit.Source = preview.Source
	audit.Target = preview.Target
	audit.PreviousState = storageStateLabel(preview.Mounted)
	if !preview.Eligibility.Allowed {
		reason := preview.Eligibility.Reason
		if reason == "" {
			reason = "The requested storage action is not eligible."
		}
		deny(403, preview.Eligibility.State, reason)
		return
	}
	audit.PolicyOutcome = "allowed"
	if insertStorageAudit(r.Context(), a.db, actor, audit) != nil {
		_ = operations.Update(r.Context(), a.db, opID, "failed", "audit persistence unavailable")
		http.Error(w, "operation audit unavailable", 503)
		return
	}
	inserted = true
	if a.beforeStorageDispatch != nil {
		a.beforeStorageDispatch()
	}
	current, principalErr := auth.PrincipalFor(r.Context(), a.db, principal.ID)
	if principalErr != nil || !current.Has(auth.PermissionStorageManage) {
		audit.Status = "rejected"
		audit.AuthorizationOutcome = "denied"
		audit.FailureReason = "Storage-management permission was revoked before dispatch."
		_ = updateStorageAudit(r.Context(), a.db, audit)
		_ = operations.Update(r.Context(), a.db, opID, "rejected", audit.FailureReason)
		http.Error(w, audit.FailureReason, 403)
		return
	}
	// Re-run the full API-side safety decision immediately before crossing the
	// privileged boundary. The helper performs the same check independently.
	if refreshed, refreshErr := a.hostStorageManager.Preview(ctx, id, body.Action); refreshErr != nil || !refreshed.Eligibility.Allowed {
		audit.Status = "rejected"
		audit.PolicyOutcome = "denied"
		audit.FailureReason = "Storage safety changed before dispatch."
		_ = updateStorageAudit(r.Context(), a.db, audit)
		_ = operations.Update(r.Context(), a.db, opID, "rejected", audit.FailureReason)
		http.Error(w, audit.FailureReason, 409)
		return
	}
	response, callErr := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: opID, OperationRevision: 1, Operation: "ManageStorageMount", StorageResourceID: id, StorageAction: string(body.Action), ConfirmationResource: body.ConfirmationResource})
	if callErr != nil {
		audit.Status = "outcome_unknown"
		audit.FailureReason = "the privileged helper connection ended before a final storage outcome was received"
		_ = updateStorageAudit(r.Context(), a.db, audit)
		_ = operations.Update(r.Context(), a.db, opID, "outcome_unknown", audit.FailureReason)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": opID, "status": "outcome_unknown", "summary": audit.FailureReason})
		return
	}
	status, summary, httpStatus := "completed", "storage action completed", http.StatusOK
	if response.OperationID != opID {
		status, summary, httpStatus = "outcome_unknown", "privileged helper returned an invalid operation receipt", http.StatusConflict
	} else if response.State == "accepted" || response.State == "executing" || response.State == "reconciling" || response.Phase == "transport_outcome_unknown" {
		status, summary, httpStatus = "outcome_unknown", "storage action was accepted, but its final state could not be confirmed", http.StatusConflict
	} else if response.ErrorCode == "StorageActionOutcomeUnknown" || response.State == "outcome_unknown" {
		status, summary, httpStatus = "outcome_unknown", "storage action outcome could not be confirmed", http.StatusConflict
	} else if response.ErrorCode == "StorageActionTimedOut" || response.State == "timed_out" {
		status, summary, httpStatus = "timed_out", "storage action timed out with an uncertain outcome", http.StatusConflict
	} else if !response.OK || response.Error != "" {
		status, summary, httpStatus = "failed", "storage action failed", http.StatusConflict
	}
	if result, valid := decodeStorageMutationResult(response.Result); valid && (result.Action == hostadmin.StorageActionMount || result.Action == hostadmin.StorageActionUnmount) {
		audit.PreviousState = storageStateLabel(result.Previous.Mounted)
		audit.ResultingState = storageStateLabel(result.Result.Mounted)
	} else if persistent, valid := decodePersistentMountResult(response.Result); valid {
		audit.PreviousState = persistent.Previous.State
		audit.ResultingState = persistent.Result.State
	}
	audit.Status = status
	if status != "completed" {
		audit.FailureReason = summary
	}
	if updateStorageAudit(r.Context(), a.db, audit) != nil {
		status, summary, httpStatus = "outcome_unknown", "storage action may have completed, but final audit persistence failed", 503
	}
	_ = operations.Update(r.Context(), a.db, opID, status, summary)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": opID, "status": status, "summary": summary, "result": response.Result})
}

func storageStateLabel(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "unmounted"
}
