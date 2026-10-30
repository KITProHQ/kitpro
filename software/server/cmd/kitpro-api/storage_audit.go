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

const maxStorageHistoryPage = 50

type storageAuditDetail struct {
	OperationID          string `json:"operation_id"`
	ResourceID           string `json:"resource_id"`
	Source               string `json:"source,omitempty"`
	Target               string `json:"target,omitempty"`
	Action               string `json:"action"`
	AuthorizationOutcome string `json:"authorization_outcome"`
	PolicyOutcome        string `json:"policy_outcome"`
	PreviousState        string `json:"previous_state,omitempty"`
	ResultingState       string `json:"resulting_state,omitempty"`
	Status               string `json:"status"`
	FailureReason        string `json:"failure_reason,omitempty"`
}
type storageHistoryItem struct {
	Timestamp     string `json:"timestamp"`
	Administrator string `json:"administrator"`
	storageAuditDetail
}
type storageHistoryPage struct {
	Items      []storageHistoryItem `json:"items"`
	NextOffset *int                 `json:"next_offset,omitempty"`
}

func storageAuditEventID(id string) string { return "storage-audit-" + id }
func insertStorageAudit(ctx context.Context, db *sql.DB, actor string, detail storageAuditDetail) error {
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 4096 {
		return errors.New("storage audit record invalid")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, storageAuditEventID(detail.OperationID), "storage-mount-operation", actor, "storage:"+detail.ResourceID, detail.Status, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func updateStorageAudit(ctx context.Context, db *sql.DB, detail storageAuditDetail) error {
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 4096 {
		return errors.New("storage audit record invalid")
	}
	result, err := db.ExecContext(ctx, `UPDATE audit_events SET outcome=?,detail_json=? WHERE event_id=? AND event_type='storage-mount-operation'`, detail.Status, string(encoded), storageAuditEventID(detail.OperationID))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("storage audit record missing")
	}
	return nil
}
func decodeStorageMutationResult(value any) (hostadmin.StorageMutationResult, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return hostadmin.StorageMutationResult{}, false
	}
	var result hostadmin.StorageMutationResult
	if json.Unmarshal(encoded, &result) != nil || !hostadmin.ValidStorageResourceID(result.ResourceID) || !hostadmin.ValidStorageAction(result.Action) {
		return hostadmin.StorageMutationResult{}, false
	}
	return result, true
}
func decodePersistentMountResult(value any) (hostadmin.PersistentMountResult, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return hostadmin.PersistentMountResult{}, false
	}
	var result hostadmin.PersistentMountResult
	if json.Unmarshal(encoded, &result) != nil || !hostadmin.ValidStorageResourceID(result.ResourceID) || !hostadmin.ValidStorageAction(result.Action) {
		return hostadmin.PersistentMountResult{}, false
	}
	return result, true
}

func (a *app) hostStorageHistory(w http.ResponseWriter, r *http.Request, id string) {
	if !readOnly(w, r) {
		return
	}
	if !hostadmin.ValidStorageResourceID(id) || !validQuery(r, "offset", "limit", "since", "until", "action", "result") {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionAuditRead); !ok {
		return
	}
	values := r.URL.Query()
	limit, err := boundedInt(values.Get("limit"), 20, maxStorageHistoryPage)
	if err != nil || limit == 0 {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	offset, err := boundedInt(values.Get("offset"), 0, 10000)
	if err != nil {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	clauses := []string{"event_type='storage-mount-operation'", "target_id=?"}
	args := []any{"storage:" + id}
	if action := values.Get("action"); action != "" {
		if !hostadmin.ValidStorageAction(hostadmin.StorageAction(action)) {
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
	for _, boundary := range []struct{ key, op string }{{"since", ">="}, {"until", "<="}} {
		if raw := values.Get(boundary.key); raw != "" {
			parsed, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				hostError(w, hostadmin.ErrInvalid)
				return
			}
			clauses = append(clauses, "created_at "+boundary.op+" ?")
			args = append(args, parsed.UTC().Format(time.RFC3339Nano))
		}
	}
	args = append(args, limit+1, offset)
	rows, err := a.db.QueryContext(r.Context(), `SELECT actor,detail_json,created_at FROM audit_events WHERE `+strings.Join(clauses, " AND ")+` ORDER BY created_at DESC,event_id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		http.Error(w, "audit history unavailable", 503)
		return
	}
	defer rows.Close()
	items := make([]storageHistoryItem, 0, limit)
	more := false
	for rows.Next() {
		var actor, encoded, created string
		if rows.Scan(&actor, &encoded, &created) != nil {
			http.Error(w, "audit history unavailable", 503)
			return
		}
		var detail storageAuditDetail
		if json.Unmarshal([]byte(encoded), &detail) != nil {
			continue
		}
		if len(items) < limit {
			items = append(items, storageHistoryItem{Timestamp: created, Administrator: actor, storageAuditDetail: detail})
		} else {
			more = true
		}
	}
	page := storageHistoryPage{Items: items}
	if more {
		next := offset + limit
		page.NextOffset = &next
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(page)
}
