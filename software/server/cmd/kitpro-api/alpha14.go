package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

type ingressMutation struct {
	InstallationID   string `json:"installation_id"`
	ServiceID        string `json:"service_id"`
	Hostname         string `json:"hostname"`
	ExposureMode     string `json:"exposure_mode"`
	UpstreamProtocol string `json:"upstream_protocol"`
	TLSPolicy        string `json:"tls_policy"`
	CertificateID    string `json:"certificate_id"`
	ProviderID       string `json:"provider_id"`
	Enabled          bool   `json:"enabled"`
}

func (a *app) ingressRoutes(w http.ResponseWriter, r *http.Request) {
	store := ingress.Store{DB: a.db}
	switch r.Method {
	case http.MethodGet:
		installationID := strings.TrimSpace(r.URL.Query().Get("installation_id"))
		routes, err := store.ListRoutes(r.Context(), installationID)
		if err != nil {
			http.Error(w, "ingress state unavailable", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
	case http.MethodPost:
		if strings.Trim(r.URL.Path, "/") != "api/v1/ingress" {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}
		var input ingressMutation
		if err := decodeStrictJSON(r, 32*1024, &input); err != nil {
			http.Error(w, "invalid ingress request", http.StatusBadRequest)
			return
		}
		hostname, err := ingress.NormalizeHostname(input.Hostname)
		if err != nil || hostname != input.Hostname {
			http.Error(w, "invalid hostname", http.StatusBadRequest)
			return
		}
		allowed, external, err := a.ingressGrant(r, input.InstallationID, input.ServiceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		routeID, err := ingress.NewID("route")
		if err != nil {
			http.Error(w, "route identity unavailable", http.StatusInternalServerError)
			return
		}
		desired := ingress.Disabled
		if input.Enabled {
			desired = ingress.Enabled
		}
		route := ingress.Route{ID: routeID, InstallationID: input.InstallationID, ServiceID: input.ServiceID, Hostname: hostname, ExposureMode: ingress.ExposureMode(input.ExposureMode), UpstreamProtocol: input.UpstreamProtocol, TLSPolicy: ingress.TLSPolicy(input.TLSPolicy), CertificateID: input.CertificateID, ProviderID: input.ProviderID, DesiredState: desired}
		if err = ingress.ValidateRoute(route, allowed, external); err != nil {
			http.Error(w, "ingress policy rejected", http.StatusBadRequest)
			return
		}
		if _, err = store.CreateRoute(r.Context(), route); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				http.Error(w, "hostname is already assigned", http.StatusConflict)
			} else {
				http.Error(w, "route could not be created", http.StatusInternalServerError)
			}
			return
		}
		if input.Enabled {
			generation, applyErr := a.applyIngress(r, store)
			if applyErr != nil {
				_, _ = a.db.ExecContext(r.Context(), `DELETE FROM ingress_routes WHERE route_id=?`, route.ID)
				http.Error(w, "ingress candidate failed; previous routes remain active", http.StatusBadGateway)
				return
			}
			route.AppliedGeneration = generation
		}
		writeJSON(w, http.StatusCreated, route)
	case http.MethodDelete:
		routeID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/ingress/"), "/")
		route, err := store.SetDesiredState(r.Context(), routeID, ingress.Disabled)
		if err != nil {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}
		if _, err = a.applyIngress(r, store); err != nil {
			_, _ = store.SetDesiredState(r.Context(), routeID, ingress.Enabled)
			http.Error(w, "ingress candidate failed; previous routes remain active", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, route)
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
	}
}

func (a *app) ingressGrant(r *http.Request, installationID, serviceID string) ([]string, bool, error) {
	var applicationID string
	if err := a.db.QueryRowContext(r.Context(), `SELECT application_id FROM installations WHERE installation_id=?`, installationID).Scan(&applicationID); err != nil {
		return nil, false, fmt.Errorf("installation not found")
	}
	entry, ok := a.catalog[applicationID]
	if !ok {
		return nil, false, fmt.Errorf("application is not trusted")
	}
	service, ok := findService(entry.Manifest, serviceID)
	if !ok || service.Ingress == nil {
		return nil, false, fmt.Errorf("service is not ingress eligible")
	}
	var mode, address, transport string
	var port int
	if err := a.db.QueryRowContext(r.Context(), `SELECT mode,host_address,host_port,transport FROM installation_service_exposure WHERE installation_id=? AND service_id=?`, installationID, serviceID).Scan(&mode, &address, &port, &transport); err != nil || mode != "loopback" || address != "127.0.0.1" || port < 1 || transport != "tcp" {
		return nil, false, fmt.Errorf("service must use a managed loopback binding before ingress")
	}
	return service.Ingress.ExposureModes, service.Ingress.External, nil
}

func findService(value manifest.Manifest, serviceID string) (manifest.Service, bool) {
	for _, service := range value.Services {
		if service.ID == serviceID {
			return service, true
		}
	}
	for _, component := range value.Components {
		for _, service := range component.Services {
			if service.ID == serviceID {
				return service, true
			}
		}
	}
	return manifest.Service{}, false
}

func (a *app) applyIngress(r *http.Request, store ingress.Store) (int64, error) {
	routes, err := store.ListRoutes(r.Context(), "")
	if err != nil {
		return 0, err
	}
	requestRoutes := make([]protocol.IngressRoute, 0, len(routes))
	activeIDs := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.DesiredState != ingress.Enabled {
			continue
		}
		requestRoutes = append(requestRoutes, protocol.IngressRoute{RouteID: route.ID, InstallationID: route.InstallationID, ServiceID: route.ServiceID, Hostname: route.Hostname, ExposureMode: string(route.ExposureMode), UpstreamProtocol: route.UpstreamProtocol, TLSPolicy: string(route.TLSPolicy), CertificateID: route.CertificateID, ProviderID: route.ProviderID})
		activeIDs = append(activeIDs, route.ID)
	}
	operationID := operations.NewID()
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: "ApplyIngressGeneration", OperationRevision: 1, IngressRoutes: requestRoutes})
	if err != nil || !response.OK {
		return 0, errors.New("helper rejected ingress generation")
	}
	var result struct {
		Generation         int64  `json:"generation"`
		PreviousGeneration int64  `json:"previous_generation"`
		SHA256             string `json:"configuration_sha256"`
	}
	encoded, _ := json.Marshal(response.Result)
	if json.Unmarshal(encoded, &result) != nil || result.Generation < 1 || len(result.SHA256) != 64 {
		return 0, errors.New("helper returned invalid ingress result")
	}
	candidate := ingress.Candidate{Generation: result.Generation, PreviousGeneration: result.PreviousGeneration, SHA256: result.SHA256}
	if err = store.RecordGeneration(r.Context(), candidate, operationID, "active", ""); err != nil {
		return 0, err
	}
	if err = store.CommitGeneration(r.Context(), candidate, activeIDs); err != nil {
		return 0, err
	}
	return result.Generation, nil
}

type certificateImport struct {
	Hostname       string `json:"hostname"`
	CertificatePEM string `json:"certificate_pem"`
	PrivateKeyPEM  string `json:"private_key_pem"`
}

func (a *app) certificates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.QueryContext(r.Context(), `SELECT certificate_id,hostname,method,state,leaf_sha256,issuer,not_before,not_after,last_renewal_at,last_error_code,created_at,updated_at FROM certificates ORDER BY hostname`)
		if err != nil {
			http.Error(w, "certificate state unavailable", 500)
			return
		}
		defer rows.Close()
		var values []map[string]string
		for rows.Next() {
			var id, hostname, method, state, digest, issuer, before, after, renewal, errorCode, created, updated string
			if rows.Scan(&id, &hostname, &method, &state, &digest, &issuer, &before, &after, &renewal, &errorCode, &created, &updated) != nil {
				http.Error(w, "certificate state unavailable", 500)
				return
			}
			values = append(values, map[string]string{"certificate_id": id, "hostname": hostname, "method": method, "state": state, "leaf_sha256": digest, "issuer": issuer, "not_before": before, "not_after": after, "last_renewal_at": renewal, "last_error_code": errorCode, "created_at": created, "updated_at": updated})
		}
		writeJSON(w, http.StatusOK, map[string]any{"certificates": values})
	case http.MethodPost:
		var input certificateImport
		if err := decodeStrictJSON(r, 384*1024, &input); err != nil {
			http.Error(w, "invalid certificate request", 400)
			return
		}
		hostname, err := ingress.NormalizeHostname(input.Hostname)
		if err != nil || hostname != input.Hostname {
			http.Error(w, "invalid hostname", 400)
			return
		}
		certificateID, err := ingress.NewID("cert")
		if err != nil {
			http.Error(w, "certificate identity unavailable", 500)
			return
		}
		response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "InstallCertificateMaterial", OperationRevision: 1, CertificateID: certificateID, Hostname: hostname, CertificatePEM: input.CertificatePEM, PrivateKeyPEM: input.PrivateKeyPEM})
		input.PrivateKeyPEM, input.CertificatePEM = "", ""
		if err != nil || !response.OK {
			http.Error(w, "certificate material was rejected", 400)
			return
		}
		var metadata struct {
			CertificateSHA256 string    `json:"certificate_sha256"`
			NotBefore         time.Time `json:"not_before"`
			NotAfter          time.Time `json:"not_after"`
		}
		encoded, _ := json.Marshal(response.Result)
		if json.Unmarshal(encoded, &metadata) != nil || len(metadata.CertificateSHA256) != 64 {
			http.Error(w, "certificate helper returned invalid metadata", 502)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = a.db.ExecContext(r.Context(), `INSERT INTO certificates(certificate_id,hostname,method,state,leaf_sha256,issuer,not_before,not_after,last_renewal_at,last_error_code,created_at,updated_at) VALUES(?,?,'provided','issued',?,'',?,?,'','',?,?)`, certificateID, hostname, metadata.CertificateSHA256, metadata.NotBefore.UTC().Format(time.RFC3339), metadata.NotAfter.UTC().Format(time.RFC3339), now, now); err != nil {
			http.Error(w, "certificate metadata could not be committed", 500)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"certificate_id": certificateID, "hostname": hostname, "method": "provided", "state": "issued", "not_before": metadata.NotBefore, "not_after": metadata.NotAfter})
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
	}
}

func decodeStrictJSON(r *http.Request, limit int64, destination any) error {
	reader := http.MaxBytesReader(nil, r.Body, limit)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// The exposure-provider stub is replaced by the Cloudflare integration.
func (a *app) exposureProviders(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (a *app) setupState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT step_id,state,evidence_json,updated_at FROM setup_steps ORDER BY step_id`)
	if err != nil {
		http.Error(w, "setup state unavailable", 500)
		return
	}
	defer rows.Close()
	steps := []map[string]any{}
	for rows.Next() {
		var id, stateValue, evidence, updated string
		if rows.Scan(&id, &stateValue, &evidence, &updated) != nil {
			http.Error(w, "setup state unavailable", 500)
			return
		}
		var safeEvidence any
		if json.Unmarshal([]byte(evidence), &safeEvidence) != nil {
			safeEvidence = map[string]any{}
		}
		steps = append(steps, map[string]any{"step_id": id, "state": stateValue, "evidence": safeEvidence, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"steps": steps, "resumable": true})
}

func (a *app) systemReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", 405)
		return
	}
	type item struct {
		Dimension string `json:"dimension"`
		State     string `json:"state"`
		Summary   string `json:"summary"`
	}
	values := []item{}
	helper, helperErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "Ping"})
	if helperErr == nil && helper.OK {
		values = append(values, item{"helper", "ready", "Privileged helper is responding"})
	} else {
		values = append(values, item{"helper", "failed", "Privileged helper is unavailable"})
	}
	runtimeResponse, runtimeErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"})
	if runtimeErr == nil && runtimeResponse.OK {
		values = append(values, item{"runtime", "ready", "Container runtime is responding"})
	} else {
		values = append(values, item{"runtime", "failed", "Container runtime is unavailable"})
	}
	var routes, certificates, providers int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_routes WHERE desired_state='enabled'`).Scan(&routes)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM certificates WHERE state IN ('issued','active','expiring','renewal-pending')`).Scan(&certificates)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM exposure_providers WHERE desired_state='enabled'`).Scan(&providers)
	values = append(values, item{"ingress", map[bool]string{true: "ready", false: "not-configured"}[routes > 0], map[bool]string{true: fmt.Sprintf("%d managed route(s)", routes), false: "No managed routes configured"}[routes > 0]})
	values = append(values, item{"tls", map[bool]string{true: "ready", false: "not-configured"}[certificates > 0], map[bool]string{true: fmt.Sprintf("%d certificate(s) available", certificates), false: "HTTPS is not configured"}[certificates > 0]})
	values = append(values, item{"provider", map[bool]string{true: "ready", false: "disabled"}[providers > 0], map[bool]string{true: "Optional exposure provider enabled", false: "Remote access is disabled"}[providers > 0]})
	writeJSON(w, 200, map[string]any{"readiness": values})
}

var _ = sql.ErrNoRows
