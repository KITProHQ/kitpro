package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func readOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}
func hostError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, hostadmin.ErrInvalid):
		http.Error(w, "invalid host query", 400)
	case errors.Is(e, hostadmin.ErrMissing):
		http.Error(w, "service not found", 404)
	case errors.Is(e, hostadmin.ErrPermission):
		http.Error(w, "host data permission denied", 403)
	case errors.Is(e, hostadmin.ErrUnsupported):
		http.Error(w, "host capability unsupported", 501)
	default:
		http.Error(w, "host capability unavailable", 503)
	}
}
func validQuery(r *http.Request, keys ...string) bool {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			return false
		}
	}
	return true
}
func boundedInt(text string, defaultValue, max int) (int, error) {
	if text == "" {
		return defaultValue, nil
	}
	n, e := strconv.Atoi(text)
	if e != nil || n < 0 || n > max {
		return 0, hostadmin.ErrInvalid
	}
	return n, nil
}
func hostContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 4*time.Second)
}
func (a *app) hostServices(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if !validQuery(r, "search", "state", "offset", "limit") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	values := r.URL.Query()
	limit, e := boundedInt(values.Get("limit"), 25, hostadmin.MaxServicePage)
	if e != nil || limit == 0 {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	offset, e := boundedInt(values.Get("offset"), 0, hostadmin.MaxServiceInventory)
	if e != nil {
		hostError(w, e)
		return
	}
	search := strings.TrimSpace(values.Get("search"))
	if strings.ContainsAny(search, "\x00\r\n") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	state := values.Get("state")
	if state == "" {
		state = "all"
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	page, e := a.hostServicesProvider.List(ctx, hostadmin.ServiceQuery{Search: search, State: state, Offset: offset, Limit: limit})
	if e != nil {
		hostError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(page)
}
func (a *app) hostServiceDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/host/services/")
	if strings.HasSuffix(path, "/history") {
		a.hostServiceHistory(w, r, strings.TrimSuffix(path, "/history"))
		return
	}
	if strings.HasSuffix(path, "/preview") {
		a.hostServicePreview(w, r, strings.TrimSuffix(path, "/preview"))
		return
	}
	if strings.HasSuffix(path, "/actions") {
		a.hostServiceAction(w, r, strings.TrimSuffix(path, "/actions"))
		return
	}
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
	name := path
	if !hostadmin.ValidUnit(name) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	item, e := a.hostServicesProvider.Detail(ctx, name)
	if e != nil {
		hostError(w, e)
		return
	}
	a.authorizeServiceActions(r, principal, &item)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(item)
}

func (a *app) hostServicePreview(w http.ResponseWriter, r *http.Request, path string) {
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	principal, ok := a.requirePermission(w, r, auth.PermissionHostRead)
	if !ok {
		return
	}
	parts := strings.Split(path, "/actions/")
	if len(parts) != 2 || !hostadmin.ValidUnit(parts[0]) || !hostadmin.ValidServiceAction(hostadmin.ServiceAction(parts[1])) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	preview, err := a.hostServicesProvider.Preview(ctx, parts[0], hostadmin.ServiceAction(parts[1]))
	if err != nil {
		hostError(w, err)
		return
	}
	if a.db != nil && preview.Eligibility.Allowed && !auth.CanManageService(r.Context(), a.db, principal, preview.Unit, string(preview.Action)) {
		preview.Eligibility.Allowed = false
		preview.Eligibility.State = "permission_denied"
		preview.Eligibility.Reason = "Your administrator account is not authorized for this service action."
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(preview)
}

type serviceActionRequest struct {
	Action           hostadmin.ServiceAction `json:"action"`
	ConfirmationUnit string                  `json:"confirmation_unit"`
}

func (a *app) hostServiceAction(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !validQuery(r) || !hostadmin.ValidUnit(name) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body serviceActionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !hostadmin.ValidServiceAction(body.Action) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	operationID := operations.NewID()
	if err := operations.Insert(r.Context(), a.db, operationID, "ManageSystemService:"+string(body.Action), name); err != nil {
		http.Error(w, "operation unavailable", http.StatusServiceUnavailable)
		return
	}
	username := principal.Username
	if username == "" {
		username = fmt.Sprintf("administrator-%d", principal.ID)
	}
	audit := serviceAuditDetail{OperationID: operationID, Unit: name, Action: string(body.Action), AuthorizationOutcome: "allowed", PolicyOutcome: "pending", Status: "running"}
	auditInserted := false
	deny := func(status int, code, reason string) {
		audit.Status = "rejected"
		audit.FailureReason = reason
		if code == "permission_denied" {
			audit.AuthorizationOutcome = "denied"
		} else {
			audit.PolicyOutcome = "denied"
		}
		if !auditInserted {
			if err := insertServiceAudit(r.Context(), a.db, username, audit); err != nil {
				_ = operations.Update(r.Context(), a.db, operationID, "failed", "audit persistence unavailable")
				http.Error(w, "operation audit unavailable", http.StatusServiceUnavailable)
				return
			}
			auditInserted = true
		}
		_ = operations.Update(r.Context(), a.db, operationID, "rejected", reason)
		slog.Default().Warn("system service operation denied", "event", "system_service_operation", "operation_id", operationID, "administrator", username, "unit", name, "action", body.Action, "authorization", code, "result", "denied")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": operationID, "status": "failed", "error_code": code, "error": reason})
	}
	if body.ConfirmationUnit != name {
		deny(http.StatusBadRequest, "confirmation_mismatch", "The confirmation must identify the exact service unit.")
		return
	}
	if !principal.Has(auth.PermissionServicesManage) || !auth.CanManageService(r.Context(), a.db, principal, name, string(body.Action)) {
		deny(http.StatusForbidden, "permission_denied", "Your administrator account is not authorized for this service action.")
		return
	}
	if !a.hostServiceManagementEnabled {
		deny(http.StatusForbidden, "permission_denied", "Service management is disabled on this server.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	service, err := a.hostServicesProvider.Detail(ctx, name)
	if err != nil {
		deny(hostErrorStatus(err), "service_unavailable", "The service could not be evaluated safely.")
		return
	}
	audit.PreviousState = service.Active
	eligibility, found := hostadmin.FindEligibility(service.Actions, body.Action)
	if !found || !eligibility.Allowed {
		reason := eligibility.Reason
		if reason == "" {
			reason = "The requested service action is not eligible."
		}
		deny(http.StatusForbidden, eligibility.State, reason)
		return
	}
	audit.PolicyOutcome = "allowed"
	if err := insertServiceAudit(r.Context(), a.db, username, audit); err != nil {
		_ = operations.Update(r.Context(), a.db, operationID, "failed", "audit persistence unavailable")
		http.Error(w, "operation audit unavailable", http.StatusServiceUnavailable)
		return
	}
	auditInserted = true
	if a.beforeServiceDispatch != nil {
		a.beforeServiceDispatch()
	}
	// Permission and exact unit/action scope are reloaded immediately before
	// dispatch. Session revocation stops later requests; this check also closes
	// the in-flight preview-to-dispatch revocation window.
	currentPrincipal, principalErr := auth.PrincipalFor(r.Context(), a.db, principal.ID)
	if principalErr != nil || !currentPrincipal.Has(auth.PermissionServicesManage) || !auth.CanManageService(r.Context(), a.db, currentPrincipal, name, string(body.Action)) {
		audit.Status = "rejected"
		audit.AuthorizationOutcome = "denied"
		audit.FailureReason = "Service-management permission was revoked before dispatch."
		if updateErr := updateServiceAudit(r.Context(), a.db, audit); updateErr != nil {
			_ = operations.Update(r.Context(), a.db, operationID, "failed", "audit persistence unavailable")
			http.Error(w, "operation audit unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = operations.Update(r.Context(), a.db, operationID, "rejected", audit.FailureReason)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": operationID, "status": "rejected", "error_code": "permission_denied", "error": audit.FailureReason})
		return
	}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, OperationRevision: 1, Operation: "ManageSystemService", UnitName: name, ServiceAction: string(body.Action), ConfirmationUnit: body.ConfirmationUnit}
	response, err := a.callHelperOperation(request)
	if err != nil {
		audit.Status = "failed"
		audit.FailureReason = "privileged helper unavailable before dispatch"
		_ = updateServiceAudit(r.Context(), a.db, audit)
		_ = operations.Update(r.Context(), a.db, operationID, "failed", audit.FailureReason)
		http.Error(w, "service operation unavailable", http.StatusServiceUnavailable)
		return
	}
	status := "completed"
	summary := "service action completed"
	httpStatus := http.StatusOK
	if response.OperationID != operationID || !validServiceHelperState(response.State) {
		status, summary, httpStatus = "outcome_unknown", "privileged helper returned an invalid operation receipt", http.StatusConflict
	} else if helperStatePending(response.State) {
		status, summary, httpStatus = "outcome_unknown", "service action outcome requires reconciliation", http.StatusAccepted
	} else if response.ErrorCode == "ServiceActionTimedOut" {
		status, summary, httpStatus = "timed_out", "service action timed out with an uncertain outcome", http.StatusConflict
	} else if response.ErrorCode == "ServiceActionOutcomeUnknown" || response.State == "action_required" {
		status, summary, httpStatus = "outcome_unknown", "service action outcome could not be confirmed", http.StatusConflict
	} else if !response.OK || response.Error != "" || response.State == "failed" || response.State == "action_required" {
		status, summary, httpStatus = "failed", "service action failed", http.StatusConflict
	}
	if result, ok := decodeServiceMutationResult(response.Result); ok {
		audit.PreviousState = result.Previous.Active
		audit.ResultingState = result.Result.Active
		audit.SystemdOutcome = result.SystemdResult
	}
	audit.Status = status
	if status != "completed" {
		audit.FailureReason = summary
	}
	if a.beforeServiceAuditFinal != nil {
		err = a.beforeServiceAuditFinal()
	} else {
		err = updateServiceAudit(r.Context(), a.db, audit)
	}
	if err != nil {
		status, summary, httpStatus = "outcome_unknown", "service action completed or may have completed, but its final audit record could not be preserved", http.StatusServiceUnavailable
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	slog.Default().Info("system service operation recorded", "event", "system_service_operation", "operation_id", operationID, "administrator", username, "unit", name, "action", body.Action, "result", status, "helper_error_code", response.ErrorCode)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": operationID, "status": status, "result": response.Result, "error_code": response.ErrorCode, "error": response.Error})
}

func validServiceHelperState(state string) bool {
	switch state {
	case "accepted", "executing", "reconciling", "succeeded", "failed", "action_required":
		return true
	}
	return false
}

func hostErrorStatus(err error) int {
	switch {
	case errors.Is(err, hostadmin.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, hostadmin.ErrMissing):
		return http.StatusNotFound
	case errors.Is(err, hostadmin.ErrPermission):
		return http.StatusForbidden
	default:
		return http.StatusServiceUnavailable
	}
}
func (a *app) hostLogs(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionHostLogsRead); !ok {
		return
	}
	if !a.hostLogsEnabled {
		http.Error(w, "journal access not configured", 403)
		return
	}
	if !validQuery(r, "unit", "priority", "since", "until", "search", "cursor", "limit") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	values := r.URL.Query()
	limit, e := boundedInt(values.Get("limit"), 50, hostadmin.MaxJournalPage)
	if e != nil || limit == 0 {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	now := time.Now().UTC()
	q := hostadmin.JournalQuery{Unit: values.Get("unit"), Search: values.Get("search"), Cursor: values.Get("cursor"), Limit: limit, Since: now.Add(-24 * time.Hour), Until: now}
	if raw := values.Get("priority"); raw != "" {
		n, err := boundedInt(raw, -1, 7)
		if err != nil || n < 0 {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		q.Priority = &n
	}
	if raw := values.Get("since"); raw != "" {
		q.Since, e = time.Parse(time.RFC3339, raw)
		if e != nil {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
	}
	if raw := values.Get("until"); raw != "" {
		q.Until, e = time.Parse(time.RFC3339, raw)
		if e != nil {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	page, e := a.hostJournalProvider.Query(ctx, q)
	if e != nil {
		hostError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(page)
}
