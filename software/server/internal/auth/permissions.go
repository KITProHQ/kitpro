package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Permission string

const (
	PermissionHostRead       Permission = "host.read"
	PermissionHostLogsRead   Permission = "host.logs.read"
	PermissionServicesManage Permission = "host.services.manage"
	PermissionStorageManage  Permission = "host.storage.manage"
	PermissionNetworkManage  Permission = "host.network.manage"
	PermissionAuditRead      Permission = "host.audit.read"
	PermissionSecurityAdmin  Permission = "host.security.admin"
)

var allPermissions = []Permission{PermissionHostRead, PermissionHostLogsRead, PermissionServicesManage, PermissionStorageManage, PermissionNetworkManage, PermissionAuditRead, PermissionSecurityAdmin}

type Principal struct {
	ID          int
	Username    string
	Permissions map[Permission]bool
}

func ValidPermission(permission Permission) bool {
	for _, candidate := range allPermissions {
		if permission == candidate {
			return true
		}
	}
	return false
}

func AllPermissions() []Permission { return append([]Permission(nil), allPermissions...) }

func PrincipalFor(ctx context.Context, db *sql.DB, administratorID int) (Principal, error) {
	var principal Principal
	principal.ID = administratorID
	principal.Permissions = map[Permission]bool{}
	if err := db.QueryRowContext(ctx, `SELECT username FROM administrator WHERE id=?`, administratorID).Scan(&principal.Username); err != nil {
		return Principal{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT permission FROM administrator_permissions WHERE administrator_id=? ORDER BY permission`, administratorID)
	if err != nil {
		return Principal{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var permission Permission
		if err = rows.Scan(&permission); err != nil {
			return Principal{}, err
		}
		if ValidPermission(permission) {
			principal.Permissions[permission] = true
		}
	}
	return principal, rows.Err()
}

func (p Principal) Has(permission Permission) bool { return p.Permissions[permission] }

// CanManageService combines the mutation permission with an exact unit/action
// scope. Security administrators retain legacy full-administrator behavior;
// restricted operators require an explicit scope row.
func CanManageService(ctx context.Context, db *sql.DB, principal Principal, unit, action string) bool {
	if !principal.Has(PermissionServicesManage) {
		return false
	}
	if principal.Has(PermissionSecurityAdmin) {
		return true
	}
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM administrator_service_scopes WHERE administrator_id=? AND unit_name=? AND action=?`, principal.ID, unit, action).Scan(&exists)
	return err == nil && exists == 1
}

func ServiceScopesFor(ctx context.Context, db *sql.DB, administratorID int) ([]ServiceScope, error) {
	rows, err := db.QueryContext(ctx, `SELECT unit_name,action FROM administrator_service_scopes WHERE administrator_id=? ORDER BY unit_name,action`, administratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ServiceScope{}
	for rows.Next() {
		var scope ServiceScope
		if err = rows.Scan(&scope.Unit, &scope.Action); err != nil {
			return nil, err
		}
		result = append(result, scope)
	}
	return result, rows.Err()
}

type PermissionChange struct {
	AdministratorID int
	SessionsRevoked int64
}

func SetPermissions(ctx context.Context, db *sql.DB, username string, requested []Permission) (PermissionChange, error) {
	if db == nil || username == "" || strings.TrimSpace(username) != username {
		return PermissionChange{}, fmt.Errorf("invalid permission change")
	}
	unique := map[Permission]bool{}
	for _, permission := range requested {
		if !ValidPermission(permission) {
			return PermissionChange{}, fmt.Errorf("invalid permission %q", permission)
		}
		unique[permission] = true
	}
	if !unique[PermissionHostRead] {
		return PermissionChange{}, fmt.Errorf("host.read is required for a local administrator")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return PermissionChange{}, err
	}
	defer tx.Rollback()
	var id int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM administrator WHERE username=?`, username).Scan(&id); err != nil {
		return PermissionChange{}, err
	}
	var hadSecurity int
	_ = tx.QueryRowContext(ctx, `SELECT 1 FROM administrator_permissions WHERE administrator_id=? AND permission=?`, id, PermissionSecurityAdmin).Scan(&hadSecurity)
	if hadSecurity == 1 && !unique[PermissionSecurityAdmin] {
		var securityAdministrators int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT administrator_id) FROM administrator_permissions WHERE permission=?`, PermissionSecurityAdmin).Scan(&securityAdministrators); err != nil {
			return PermissionChange{}, err
		}
		if securityAdministrators <= 1 {
			return PermissionChange{}, fmt.Errorf("cannot remove the final security administrator")
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM administrator_permissions WHERE administrator_id=?`, id); err != nil {
		return PermissionChange{}, err
	}
	ordered := make([]string, 0, len(unique))
	for permission := range unique {
		ordered = append(ordered, string(permission))
	}
	sort.Strings(ordered)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, permission := range ordered {
		if _, err = tx.ExecContext(ctx, `INSERT INTO administrator_permissions(administrator_id,permission,granted_at) VALUES(?,?,?)`, id, permission, now); err != nil {
			return PermissionChange{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE administrator_id=? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return PermissionChange{}, err
	}
	revoked, _ := result.RowsAffected()
	detail, _ := json.Marshal(map[string]any{"permissions": ordered, "sessions_revoked": revoked})
	eventID := fmt.Sprintf("audit-admin-permissions-%d", time.Now().UTC().UnixNano())
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, eventID, "administrator-permissions-changed", "local-root", fmt.Sprintf("administrator-%d", id), "succeeded", string(detail), now); err != nil {
		return PermissionChange{}, err
	}
	if err = tx.Commit(); err != nil {
		return PermissionChange{}, err
	}
	return PermissionChange{AdministratorID: id, SessionsRevoked: revoked}, nil
}

type ServiceScope struct {
	Unit   string `json:"unit"`
	Action string `json:"action"`
}

func SetServiceScopes(ctx context.Context, db *sql.DB, username string, scopes []ServiceScope) (PermissionChange, error) {
	if db == nil || username == "" || strings.TrimSpace(username) != username || len(scopes) > 64 {
		return PermissionChange{}, fmt.Errorf("invalid service scope change")
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		key := scope.Unit + "\x00" + scope.Action
		if !validScopedUnit(scope.Unit) || !validScopedAction(scope.Action) || seen[key] {
			return PermissionChange{}, fmt.Errorf("invalid service scope")
		}
		seen[key] = true
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return PermissionChange{}, err
	}
	defer tx.Rollback()
	var id int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM administrator WHERE username=?`, username).Scan(&id); err != nil {
		return PermissionChange{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM administrator_service_scopes WHERE administrator_id=?`, id); err != nil {
		return PermissionChange{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, scope := range scopes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO administrator_service_scopes(administrator_id,unit_name,action,granted_at) VALUES(?,?,?,?)`, id, scope.Unit, scope.Action, now); err != nil {
			return PermissionChange{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE administrator_id=? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return PermissionChange{}, err
	}
	revoked, _ := result.RowsAffected()
	detail, _ := json.Marshal(map[string]any{"scope_count": len(scopes), "sessions_revoked": revoked})
	eventID := fmt.Sprintf("audit-admin-scopes-%d", time.Now().UTC().UnixNano())
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, eventID, "administrator-service-scopes-changed", "local-root", fmt.Sprintf("administrator-%d", id), "succeeded", string(detail), now); err != nil {
		return PermissionChange{}, err
	}
	if err = tx.Commit(); err != nil {
		return PermissionChange{}, err
	}
	return PermissionChange{AdministratorID: id, SessionsRevoked: revoked}, nil
}

func validScopedAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "reload", "enable", "disable":
		return true
	}
	return false
}

func validScopedUnit(unit string) bool {
	if len(unit) < 9 || len(unit) > 200 || !strings.HasSuffix(unit, ".service") || strings.ContainsAny(unit, "/\\\x00\r\n") {
		return false
	}
	for _, r := range unit {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.@:-", r)) {
			return false
		}
	}
	return true
}
