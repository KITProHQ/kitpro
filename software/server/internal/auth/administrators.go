package auth

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type AdministratorRemoval struct {
	AdministratorID int
	SessionsRevoked int64
}

// CreateAdministrator creates an additional local administrator. It is meant
// for the root-only maintenance command; callers must enforce that boundary.
func CreateAdministrator(ctx context.Context, db *sql.DB, username, password string) (int, error) {
	if db == nil || username == "" || strings.TrimSpace(username) != username || len(username) > 128 || !validPassword(password) {
		return 0, fmt.Errorf("invalid administrator creation request")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordHashCost)
	if err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO administrator(username,password_hash,created_at,updated_at) VALUES(?,?,?,?)`, username, string(hash), now, now)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO administrator_permissions(administrator_id,permission,granted_at) VALUES(?,?,?)`, id, PermissionHostRead, now); err != nil {
		return 0, err
	}
	eventID := fmt.Sprintf("audit-admin-create-%d", time.Now().UTC().UnixNano())
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,'{}',?)`, eventID, "administrator-created", "local-root", fmt.Sprintf("administrator-%d", id), "succeeded", now); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int(id), nil
}

// RemoveAdministrator removes one additional local administrator and its
// sessions. The final administrator is protected from removal.
func RemoveAdministrator(ctx context.Context, db *sql.DB, username string) (AdministratorRemoval, error) {
	if db == nil || username == "" || strings.TrimSpace(username) != username {
		return AdministratorRemoval{}, fmt.Errorf("invalid administrator removal request")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AdministratorRemoval{}, err
	}
	defer tx.Rollback()
	var id, count int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM administrator WHERE username=?`, username).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return AdministratorRemoval{}, fmt.Errorf("administrator not found")
		}
		return AdministratorRemoval{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM administrator`).Scan(&count); err != nil {
		return AdministratorRemoval{}, err
	}
	if count <= 1 {
		return AdministratorRemoval{}, fmt.Errorf("cannot remove the final administrator")
	}
	var targetSecurity, securityAdministrators int
	_ = tx.QueryRowContext(ctx, `SELECT 1 FROM administrator_permissions WHERE administrator_id=? AND permission=?`, id, PermissionSecurityAdmin).Scan(&targetSecurity)
	if targetSecurity == 1 {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT administrator_id) FROM administrator_permissions WHERE permission=?`, PermissionSecurityAdmin).Scan(&securityAdministrators); err != nil {
			return AdministratorRemoval{}, err
		}
		if securityAdministrators <= 1 {
			return AdministratorRemoval{}, fmt.Errorf("cannot remove the final security administrator")
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE administrator_id=? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return AdministratorRemoval{}, err
	}
	revoked, _ := result.RowsAffected()
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE administrator_id=?`, id); err != nil {
		return AdministratorRemoval{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM administrator WHERE id=?`, id); err != nil {
		return AdministratorRemoval{}, err
	}
	eventID := fmt.Sprintf("audit-admin-remove-%d", time.Now().UTC().UnixNano())
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,'{}',?)`, eventID, "administrator-removed", "local-root", fmt.Sprintf("administrator-%d", id), "succeeded", now); err != nil {
		return AdministratorRemoval{}, err
	}
	if err = tx.Commit(); err != nil {
		return AdministratorRemoval{}, err
	}
	return AdministratorRemoval{AdministratorID: id, SessionsRevoked: revoked}, nil
}
