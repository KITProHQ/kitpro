package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

const maxNetworkHistoryPage = 50

type networkAuditDetail struct {
	TransactionID        string `json:"transaction_id"`
	CorrelationID        string `json:"correlation_id,omitempty"`
	ResourceID           string `json:"resource_id"`
	Interface            string `json:"interface"`
	Action               string `json:"action"`
	AuthorizationOutcome string `json:"authorization_outcome"`
	PolicyOutcome        string `json:"policy_outcome"`
	PreviousDigest       string `json:"previous_digest,omitempty"`
	CandidateDigest      string `json:"candidate_digest,omitempty"`
	State                string `json:"state"`
	ConfirmationDeadline string `json:"confirmation_deadline,omitempty"`
	RollbackResult       string `json:"rollback_result,omitempty"`
	FailureReason        string `json:"failure_reason,omitempty"`
}
type networkHistoryItem struct {
	Timestamp     string `json:"timestamp"`
	Administrator string `json:"administrator"`
	networkAuditDetail
}

func networkAuditEventID(id string) string { return "network-audit-" + id }
func insertNetworkAudit(ctx context.Context, db *sql.DB, actor string, detail networkAuditDetail) error {
	raw, err := json.Marshal(detail)
	if err != nil || len(raw) > 4096 {
		return errors.New("network audit record invalid")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, networkAuditEventID(detail.TransactionID), "network-configuration-operation", actor, "network:"+detail.ResourceID, detail.State, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func updateNetworkAudit(ctx context.Context, db *sql.DB, detail networkAuditDetail) error {
	raw, err := json.Marshal(detail)
	if err != nil || len(raw) > 4096 {
		return errors.New("network audit record invalid")
	}
	result, err := db.ExecContext(ctx, `UPDATE audit_events SET outcome=?,detail_json=? WHERE event_id=? AND event_type='network-configuration-operation'`, detail.State, string(raw), networkAuditEventID(detail.TransactionID))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("network audit record missing")
	}
	return nil
}
func (a *app) hostNetworkHistory(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r, "resource", "offset", "limit", "since", "until", "action", "result") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionAuditRead); !ok {
		return
	}
	values := r.URL.Query()
	limit, err := boundedInt(values.Get("limit"), 20, maxNetworkHistoryPage)
	if err != nil || limit == 0 {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	offset, err := boundedInt(values.Get("offset"), 0, 10000)
	if err != nil {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	resource := values.Get("resource")
	if resource != "" && !hostadmin.ValidNetworkResourceID(resource) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	clauses := []string{"event_type='network-configuration-operation'"}
	args := []any{}
	if resource != "" {
		clauses = append(clauses, "target_id=?")
		args = append(args, "network:"+resource)
	}
	if action := values.Get("action"); action != "" {
		if action != "apply" && action != "confirm" && action != "rollback" {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		clauses = append(clauses, "json_extract(detail_json,'$.action')=?")
		args = append(args, action)
	}
	if result := values.Get("result"); result != "" {
		if !validNetworkAuditState(result) {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		clauses = append(clauses, "outcome=?")
		args = append(args, result)
	}
	for _, boundary := range []struct{ key, operator string }{{"since", ">="}, {"until", "<="}} {
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
	query := `SELECT actor,detail_json,created_at FROM audit_events WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY created_at DESC,event_id DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	defer rows.Close()
	items := []networkHistoryItem{}
	for rows.Next() {
		var actor, raw, created string
		if rows.Scan(&actor, &raw, &created) != nil {
			http.Error(w, "audit unavailable", 503)
			return
		}
		var detail networkAuditDetail
		if json.Unmarshal([]byte(raw), &detail) != nil {
			continue
		}
		items = append(items, networkHistoryItem{Timestamp: created, Administrator: actor, networkAuditDetail: detail})
	}
	if err = rows.Close(); err != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	for index := range items {
		items[index].networkAuditDetail = a.reconcileNetworkAudit(r.Context(), items[index].networkAuditDetail)
	}
	var next *int
	if len(items) > limit {
		items = items[:limit]
		value := offset + limit
		next = &value
	}
	writeNetworkJSON(w, map[string]any{"items": items, "next_offset": next})
}

func validNetworkAuditState(value string) bool {
	switch value {
	case "rejected", "running", "awaiting_confirmation", "confirmed", "rolled_back", "failed", "rollback_failed", "timed_out", "outcome_unknown":
		return true
	}
	return false
}

func actorName(principal auth.Principal) string {
	if strings.TrimSpace(principal.Username) != "" {
		return principal.Username
	}
	return "administrator-" + strconv.Itoa(principal.ID)
}
