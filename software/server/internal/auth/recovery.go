package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type RecoveryResult struct {
	AdministratorID int
	SessionsRevoked int64
}

// ResetPassword is the storage operation used only by the local privileged
// recovery command. Callers must enforce local root authority before invoking
// it. The old credential is replaced, never disclosed.
func ResetPassword(ctx context.Context, db *sql.DB, username, next string) (RecoveryResult, error) {
	if db == nil || username == "" || !validPassword(next) {
		return RecoveryResult{}, fmt.Errorf("invalid password recovery request")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), PasswordHashCost)
	if err != nil {
		return RecoveryResult{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer tx.Rollback()
	var administratorID int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM administrator WHERE username=?`, username).Scan(&administratorID); err != nil {
		if err == sql.ErrNoRows {
			return RecoveryResult{}, fmt.Errorf("administrator not found")
		}
		return RecoveryResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE administrator SET password_hash=?,updated_at=? WHERE id=?`, string(hash), now, administratorID); err != nil {
		return RecoveryResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE administrator_id=? AND revoked_at IS NULL`, now, administratorID)
	if err != nil {
		return RecoveryResult{}, err
	}
	revoked, _ := result.RowsAffected()
	eventID := fmt.Sprintf("audit-admin-reset-%d", time.Now().UTC().UnixNano())
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,'{}',?)`, eventID, "administrator-password-reset", "local-root", fmt.Sprintf("administrator-%d", administratorID), "succeeded", now); err != nil {
		return RecoveryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{AdministratorID: administratorID, SessionsRevoked: revoked}, nil
}
