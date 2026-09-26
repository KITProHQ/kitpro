package main

import (
	"context"
	"database/sql"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/diagnostics"
	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func (a *app) diagnostics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.QueryContext(r.Context(), `SELECT bundle_id,status,filename,archive_sha256,created_at,COALESCE(completed_at,''),error_code FROM support_bundles ORDER BY created_at DESC LIMIT 20`)
		if err != nil {
			http.Error(w, "support bundle state unavailable", 500)
			return
		}
		defer rows.Close()
		values := []map[string]string{}
		for rows.Next() {
			var id, status, filename, digest, created, completed, errorCode string
			if rows.Scan(&id, &status, &filename, &digest, &created, &completed, &errorCode) != nil {
				http.Error(w, "support bundle state unavailable", 500)
				return
			}
			values = append(values, map[string]string{"bundle_id": id, "status": status, "filename": filename, "sha256": digest, "created_at": created, "completed_at": completed, "error_code": errorCode})
		}
		writeJSON(w, 200, map[string]any{"bundles": values, "includes": []string{"build and OS identity", "runtime status", "installations and service bindings", "ingress, certificate, and provider status", "recent operation outcomes"}, "excludes": []string{"passwords", "application secrets", "API tokens", "Cloudflare credentials", "certificate private keys", "cookies and session material", "authorization headers"}})
	case http.MethodPost:
		a.generateSupportBundle(w, r)
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
	}
}

func (a *app) generateSupportBundle(w http.ResponseWriter, r *http.Request) {
	bundleID, err := ingress.NewID("bundle")
	if err != nil {
		http.Error(w, "support bundle identity unavailable", 500)
		return
	}
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	if _, err = a.db.ExecContext(r.Context(), `INSERT INTO support_bundles(bundle_id,status,created_at) VALUES(?,'collecting',?)`, bundleID, nowText); err != nil {
		http.Error(w, "support bundle could not be started", 500)
		return
	}
	sections := map[string]any{
		"system": map[string]any{"version": buildinfo.Version, "source_commit": buildinfo.SourceCommit, "build_date": buildinfo.BuildDate, "os": runtime.GOOS, "architecture": runtime.GOARCH, "platform": platformName(), "package_family": packageFamily()},
	}
	if response, callErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"}); callErr == nil && response.OK {
		sections["runtime"] = response.Result
	} else {
		sections["runtime"] = map[string]any{"state": "unavailable"}
	}
	queries := map[string]string{
		"installations":    `SELECT installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at FROM installations ORDER BY installation_id`,
		"service-bindings": `SELECT installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at FROM installation_service_exposure ORDER BY installation_id,service_id`,
		"ingress":          `SELECT route_id,installation_id,service_id,hostname,exposure_mode,upstream_protocol,tls_policy,certificate_id,provider_id,desired_state,applied_generation,created_at,updated_at FROM ingress_routes ORDER BY hostname`,
		"certificates":     `SELECT certificate_id,hostname,method,state,leaf_sha256,issuer,not_before,not_after,last_renewal_at,last_error_code,created_at,updated_at FROM certificates ORDER BY hostname`,
		"providers":        `SELECT provider_id,provider_type,account_id,zone_id,tunnel_id,credential_sha256,desired_state,runtime_state,last_error_code,created_at,updated_at FROM exposure_providers ORDER BY provider_id`,
		"operations":       `SELECT operation_id,operation_type,target_id,status,summary,created_at,updated_at FROM operations ORDER BY created_at DESC LIMIT 100`,
		"readiness":        `SELECT entity_type,entity_id,dimension,state,reason_code,summary,checked_at FROM readiness_observations ORDER BY entity_type,entity_id,dimension`,
	}
	for name, query := range queries {
		values, queryErr := queryRows(r.Context(), a.db, query)
		if queryErr != nil {
			sections[name] = map[string]any{"state": "unavailable"}
		} else {
			sections[name] = values
		}
	}
	result, err := (diagnostics.Builder{OutputRoot: a.supportRoot}).Build(diagnostics.Input{BundleID: bundleID, Created: now, Sections: sections}, nil)
	if err != nil {
		status := "failed"
		code := "generation_failed"
		if strings.Contains(err.Error(), "redaction") || strings.Contains(err.Error(), "private-key") {
			status, code = "redaction-failed", "redaction_failed"
		}
		_, _ = a.db.ExecContext(r.Context(), `UPDATE support_bundles SET status=?,completed_at=?,error_code=? WHERE bundle_id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), code, bundleID)
		http.Error(w, "support bundle generation failed", 500)
		return
	}
	_, err = a.db.ExecContext(r.Context(), `UPDATE support_bundles SET status='complete',filename=?,archive_sha256=?,completed_at=?,error_code='' WHERE bundle_id=?`, result.Filename, result.SHA256, time.Now().UTC().Format(time.RFC3339Nano), bundleID)
	if err != nil {
		http.Error(w, "support bundle state could not be committed", 500)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func queryRows(ctx context.Context, db *sql.DB, query string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err = rows.Scan(pointers...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for index, column := range columns {
			if raw, ok := values[index].([]byte); ok {
				item[column] = string(raw)
			} else {
				item[column] = values[index]
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
