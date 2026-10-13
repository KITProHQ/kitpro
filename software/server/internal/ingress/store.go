package ingress

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Store struct{ DB *sql.DB }

func (s Store) CreateRoute(ctx context.Context, route Route) (Route, error) {
	if s.DB == nil {
		return Route{}, fmt.Errorf("ingress store is not configured")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	route.CreatedAt, route.UpdatedAt = now, now
	_, err := s.DB.ExecContext(ctx, `INSERT INTO ingress_routes(route_id,installation_id,service_id,hostname,exposure_mode,upstream_protocol,tls_policy,certificate_id,provider_id,desired_state,applied_generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, route.ID, route.InstallationID, route.ServiceID, route.Hostname, route.ExposureMode, route.UpstreamProtocol, route.TLSPolicy, route.CertificateID, route.ProviderID, route.DesiredState, route.AppliedGeneration, route.CreatedAt, route.UpdatedAt)
	if err != nil {
		return Route{}, fmt.Errorf("create ingress route: %w", err)
	}
	return route, nil
}

func (s Store) GetRoute(ctx context.Context, routeID string) (Route, error) {
	var route Route
	err := s.DB.QueryRowContext(ctx, `SELECT route_id,installation_id,service_id,hostname,exposure_mode,upstream_protocol,tls_policy,certificate_id,provider_id,desired_state,applied_generation,created_at,updated_at FROM ingress_routes WHERE route_id=?`, routeID).Scan(&route.ID, &route.InstallationID, &route.ServiceID, &route.Hostname, &route.ExposureMode, &route.UpstreamProtocol, &route.TLSPolicy, &route.CertificateID, &route.ProviderID, &route.DesiredState, &route.AppliedGeneration, &route.CreatedAt, &route.UpdatedAt)
	return route, err
}

func (s Store) ListRoutes(ctx context.Context, installationID string) ([]Route, error) {
	query := `SELECT route_id,installation_id,service_id,hostname,exposure_mode,upstream_protocol,tls_policy,certificate_id,provider_id,desired_state,applied_generation,created_at,updated_at FROM ingress_routes`
	args := []any{}
	if installationID != "" {
		query += ` WHERE installation_id=?`
		args = append(args, installationID)
	}
	query += ` ORDER BY hostname,route_id`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []Route
	for rows.Next() {
		var route Route
		if err := rows.Scan(&route.ID, &route.InstallationID, &route.ServiceID, &route.Hostname, &route.ExposureMode, &route.UpstreamProtocol, &route.TLSPolicy, &route.CertificateID, &route.ProviderID, &route.DesiredState, &route.AppliedGeneration, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (s Store) SetDesiredState(ctx context.Context, routeID string, state DesiredState) (Route, error) {
	if state != Enabled && state != Disabled {
		return Route{}, fmt.Errorf("invalid desired state")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE ingress_routes SET desired_state=?,updated_at=? WHERE route_id=?`, state, time.Now().UTC().Format(time.RFC3339Nano), routeID)
	if err != nil {
		return Route{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Route{}, sql.ErrNoRows
	}
	return s.GetRoute(ctx, routeID)
}

func (s Store) NextGeneration(ctx context.Context) (int64, int64, error) {
	var next, previous int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM ingress_generations`).Scan(&next); err != nil {
		return 0, 0, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM ingress_generations WHERE status='active'`).Scan(&previous); err != nil {
		return 0, 0, err
	}
	return next, previous, nil
}

func (s Store) RecordGeneration(ctx context.Context, candidate Candidate, operationID, status, failureCode string) error {
	if status != "prepared" && status != "validated" && status != "activating" && status != "active" && status != "failed" && status != "rollback_pending" && status != "rolled_back" && status != "cleanup_pending" {
		return fmt.Errorf("invalid generation status")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,previous_generation,created_at,updated_at,failure_code) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(generation) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,failure_code=excluded.failure_code`, candidate.Generation, operationID, candidate.SHA256, status, nullableGeneration(candidate.PreviousGeneration), now, now, failureCode)
	return err
}

func (s Store) CommitGeneration(ctx context.Context, candidate Candidate, routeIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if candidate.PreviousGeneration > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE ingress_generations SET status='rolled_back',updated_at=? WHERE generation=? AND status='active'`, now, candidate.PreviousGeneration); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE ingress_generations SET status='active',updated_at=?,failure_code='' WHERE generation=?`, now, candidate.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("candidate generation not found")
	}
	for _, routeID := range routeIDs {
		if !routePattern.MatchString(routeID) {
			return fmt.Errorf("invalid route identity")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE ingress_routes SET applied_generation=?,updated_at=? WHERE route_id=? AND desired_state='enabled'`, candidate.Generation, now, routeID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullableGeneration(generation int64) any {
	if generation == 0 {
		return nil
	}
	return generation
}
