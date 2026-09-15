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

// RecoverAccepted closes operations whose in-process helper call was lost when
// the API restarted. The desired installation remains available for an
// explicit, idempotent recreate; success is never inferred after a crash.
func RecoverAccepted(ctx context.Context, db *sql.DB) (int64, error) {
	result, err := db.ExecContext(ctx, "UPDATE operations SET status='failed',summary='interrupted by API restart; retry safely' WHERE status='accepted'")
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
