package exposure

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

var identityPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)

type Record struct {
	InstallationID string    `json:"installation_id"`
	ServiceID      string    `json:"service_id"`
	Transport      Transport `json:"transport"`
	Mode           Mode      `json:"mode"`
	HostAddress    string    `json:"host_address,omitempty"`
	HostPort       int       `json:"host_port,omitempty"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
}

func Get(ctx context.Context, db *sql.DB, installationID, serviceID string) (Record, error) {
	var r Record
	err := db.QueryRowContext(ctx, `SELECT installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at FROM installation_service_exposure WHERE installation_id=? AND service_id=?`, installationID, serviceID).Scan(&r.InstallationID, &r.ServiceID, &r.Transport, &r.Mode, &r.HostAddress, &r.HostPort, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func List(ctx context.Context, db *sql.DB, installationID string) ([]Record, error) {
	rows, err := db.QueryContext(ctx, `SELECT installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at FROM installation_service_exposure WHERE installation_id=? ORDER BY service_id`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.InstallationID, &r.ServiceID, &r.Transport, &r.Mode, &r.HostAddress, &r.HostPort, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func Upsert(ctx context.Context, db *sql.DB, installationID, serviceID string, assignment Assignment, configuredLAN string) (Record, error) {
	return UpsertBinding(ctx, db, installationID, ServiceBinding{ServiceID: serviceID, Transport: TCP, Mode: assignment.Mode, HostAddress: assignment.Address, HostPort: assignment.Port}, configuredLAN, 0)
}

func UpsertBinding(ctx context.Context, db *sql.DB, installationID string, binding ServiceBinding, configuredLAN string, fixedHostPort int) (Record, error) {
	serviceID := binding.ServiceID
	if !identityPattern.MatchString(installationID) || !identityPattern.MatchString(serviceID) {
		return Record{}, fmt.Errorf("invalid exposure identity")
	}
	if binding.ContainerPort == 0 {
		binding.ContainerPort = 1
	}
	if err := ValidateBinding(binding, configuredLAN, fixedHostPort); err != nil {
		return Record{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `INSERT INTO installation_service_exposure(installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(installation_id,service_id) DO UPDATE SET transport=excluded.transport,mode=excluded.mode,host_address=excluded.host_address,host_port=excluded.host_port,updated_at=excluded.updated_at`, installationID, serviceID, binding.Transport, binding.Mode, binding.HostAddress, binding.HostPort, now, now)
	if err != nil {
		return Record{}, err
	}
	return Get(ctx, db, installationID, serviceID)
}
