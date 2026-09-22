package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type Record struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	RequestedAt string `json:"requested_at"`
	Status      string `json:"status"`
	// InstanceID is the stable installation identity. Runtime generations are
	// stored separately and are never inferred from this field.
	InstanceID string `json:"instance_id"`
	Summary    string `json:"summary"`
}

func NewID() string { b := make([]byte, 16); _, _ = rand.Read(b); return "op-" + hex.EncodeToString(b) }

// NewRequestID identifies one transport exchange. It must never be reused as
// the semantic idempotency key for privileged work.
func NewRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "req-" + hex.EncodeToString(b)
}

// NewInstallation returns a stable identity for an installed application.
func NewInstallation() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "inst-" + hex.EncodeToString(b)
}

// NewInstance is retained as a compatibility alias for older callers.
func NewInstance() string { return NewInstallation() }
func Insert(ctx context.Context, db *sql.DB, id, typ, instance string) error {
	_, e := db.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?,?,?)", id, typ, time.Now().UTC().Format(time.RFC3339Nano), "accepted", instance, "")
	return e
}
func Update(ctx context.Context, db *sql.DB, id, status, summary string) error {
	_, e := db.ExecContext(ctx, "UPDATE operations SET status=?,summary=? WHERE id=?", status, summary, id)
	return e
}

// RecoverAccepted intentionally preserves accepted projections. The helper is
// authoritative for privileged execution and the API can resolve these records
// through GetOperation after restart; API process death is not execution proof.
func RecoverAccepted(ctx context.Context, db *sql.DB) (int64, error) {
	result, err := db.ExecContext(ctx, "UPDATE operations SET summary=CASE WHEN summary='' THEN 'awaiting helper state' ELSE summary END WHERE status='accepted'")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func Get(ctx context.Context, db *sql.DB, id string) (Record, error) {
	var r Record
	e := db.QueryRowContext(ctx, "SELECT id,type,requested_at,status,instance_id,summary FROM operations WHERE id=?", id).Scan(&r.ID, &r.Type, &r.RequestedAt, &r.Status, &r.InstanceID, &r.Summary)
	return r, e
}
func List(ctx context.Context, db *sql.DB) ([]Record, error) {
	rows, e := db.QueryContext(ctx, "SELECT id,type,requested_at,status,instance_id,summary FROM operations ORDER BY requested_at DESC LIMIT 20")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if e = rows.Scan(&r.ID, &r.Type, &r.RequestedAt, &r.Status, &r.InstanceID, &r.Summary); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ = fmt.Sprintf
