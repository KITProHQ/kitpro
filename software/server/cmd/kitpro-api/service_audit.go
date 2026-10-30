package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

const maxServiceHistoryPage = 50

type serviceAuditDetail struct {
	OperationID          string `json:"operation_id"`
	Unit                 string `json:"unit"`
	Action               string `json:"action"`
	AuthorizationOutcome string `json:"authorization_outcome"`
	PolicyOutcome        string `json:"policy_outcome"`
	PreviousState        string `json:"previous_state,omitempty"`
	ResultingState       string `json:"resulting_state,omitempty"`
	SystemdOutcome       string `json:"systemd_job_outcome,omitempty"`
	Status               string `json:"status"`
	FailureReason        string `json:"failure_reason,omitempty"`
}

type serviceHistoryItem struct {
	Timestamp            string `json:"timestamp"`
	Administrator        string `json:"administrator"`
	Unit                 string `json:"unit"`
	Action               string `json:"action"`
	AuthorizationOutcome string `json:"authorization_outcome"`
	PolicyOutcome        string `json:"policy_outcome"`
	PreviousState        string `json:"previous_state,omitempty"`
	ResultingState       string `json:"resulting_state,omitempty"`
	SystemdOutcome       string `json:"systemd_job_outcome,omitempty"`
	Status               string `json:"status"`
	FailureReason        string `json:"failure_reason,omitempty"`
	OperationID          string `json:"operation_id"`
}

type serviceHistoryPage struct {
	Items      []serviceHistoryItem `json:"items"`
	NextOffset *int                 `json:"next_offset,omitempty"`
}

func serviceAuditEventID(operationID string) string { return "service-audit-" + operationID }

func insertServiceAudit(ctx context.Context, db *sql.DB, actor string, detail serviceAuditDetail) error {
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 4096 {
		return errors.New("service audit record invalid")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, serviceAuditEventID(detail.OperationID), "system-service-operation", actor, "systemd:"+detail.Unit, detail.Status, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func updateServiceAudit(ctx context.Context, db *sql.DB, detail serviceAuditDetail) error {
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 4096 {
		return errors.New("service audit record invalid")
	}
	result, err := db.ExecContext(ctx, `UPDATE audit_events SET outcome=?,detail_json=? WHERE event_id=? AND event_type='system-service-operation'`, detail.Status, string(encoded), serviceAuditEventID(detail.OperationID))
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("service audit record missing")
	}
	return nil
}

func decodeServiceMutationResult(value any) (hostadmin.ServiceMutationResult, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return hostadmin.ServiceMutationResult{}, false
	}
	var result hostadmin.ServiceMutationResult
	if json.Unmarshal(encoded, &result) != nil || !hostadmin.ValidUnit(result.Unit) || !hostadmin.ValidServiceAction(result.Action) {
		return hostadmin.ServiceMutationResult{}, false
	}
	return result, true
}

func (a *app) hostServiceHistory(w http.ResponseWriter, r *http.Request, name string) {
	if !readOnly(w, r) {
		return
	}
	if !hostadmin.ValidUnit(name) || !validQuery(r, "offset", "limit", "since", "until", "action", "result") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionAuditRead); !ok {
		return
	}
	values := r.URL.Query()
	limit, err := boundedInt(values.Get("limit"), 20, maxServiceHistoryPage)
	if err != nil || limit == 0 {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	offset, err := boundedInt(values.Get("offset"), 0, 10000)
	if err != nil {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	clauses := []string{"event_type='system-service-operation'", "target_id=?"}
	args := []any{"systemd:" + name}
	if action := values.Get("action"); action != "" {
		if !hostadmin.ValidServiceAction(hostadmin.ServiceAction(action)) {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		clauses = append(clauses, "json_extract(detail_json,'$.action')=?")
		args = append(args, action)
	}
	if result := values.Get("result"); result != "" {
		if !validServiceAuditStatus(result) {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		clauses = append(clauses, "outcome=?")
		args = append(args, result)
	}
	for _, boundary := range []struct {
		key, operator string
	}{{"since", ">="}, {"until", "<="}} {
		if raw := values.Get(boundary.key); raw != "" {
			parsed, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				hostError(w, hostadmin.ErrInvalid)
				return
			}
			clauses = append(clauses, "created_at "+boundary.operator+" ?")
			args = append(args, parsed.UTC().Format(time.RFC3339Nano))
		}
	}
	args = append(args, limit+1, offset)
	query := `SELECT actor,detail_json,created_at FROM audit_events WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY created_at DESC,event_id DESC LIMIT ? OFFSET ?`
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "audit history unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	items := make([]serviceHistoryItem, 0, limit)
	hasMore := false
	for rows.Next() {
		var actor, encoded, created string
		if err = rows.Scan(&actor, &encoded, &created); err != nil {
			http.Error(w, "audit history unavailable", http.StatusServiceUnavailable)
			return
		}
		var detail serviceAuditDetail
		if json.Unmarshal([]byte(encoded), &detail) != nil {
			continue
		}
		if len(items) < limit {
			items = append(items, serviceHistoryItem{Timestamp: created, Administrator: actor, Unit: detail.Unit, Action: detail.Action, AuthorizationOutcome: detail.AuthorizationOutcome, PolicyOutcome: detail.PolicyOutcome, PreviousState: detail.PreviousState, ResultingState: detail.ResultingState, SystemdOutcome: detail.SystemdOutcome, Status: detail.Status, FailureReason: detail.FailureReason, OperationID: detail.OperationID})
		} else {
			hasMore = true
		}
	}
	if err = rows.Err(); err != nil {
		http.Error(w, "audit history unavailable", http.StatusServiceUnavailable)
		return
	}
	page := serviceHistoryPage{Items: items}
	if hasMore {
		next := offset + limit
		page.NextOffset = &next
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(page)
}

func validServiceAuditStatus(status string) bool {
	switch status {
	case "rejected", "accepted", "running", "completed", "failed", "timed_out", "outcome_unknown":
		return true
	}
	return false
}
