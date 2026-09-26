package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/readiness"
	"golang.org/x/sys/unix"
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
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/v1/ingress/reconcile" {
		routes, err := store.ListRoutes(r.Context(), "")
		if err != nil {
			http.Error(w, "ingress state unavailable", http.StatusInternalServerError)
			return
		}
		validated := 0
		heldRoutes := []ingress.Route{}
		for _, route := range routes {
			var held int
			_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_route_holds WHERE route_id=?`, route.ID).Scan(&held)
			if route.DesiredState == ingress.Enabled && held > 0 {
				validated++
				heldRoutes = append(heldRoutes, route)
				if a.observeRouteReadiness(r.Context(), route).State != "ready" {
					http.Error(w, "ingress remains disabled because a backend is not ready", http.StatusConflict)
					return
				}
			}
		}
		if _, err = a.db.ExecContext(r.Context(), `DELETE FROM ingress_route_holds WHERE reason='application-restore'`); err != nil {
			http.Error(w, "ingress restore holds could not be released", http.StatusInternalServerError)
			return
		}
		generation, err := a.applyIngress(r, store)
		if err != nil {
			_ = setRouteHolds(r.Context(), a.db, heldRoutes, true)
			_, _ = a.applyIngress(r, store)
			http.Error(w, "ingress reconciliation failed; bootstrap or previous generation remains active", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"generation": generation, "routes_validated": validated})
		return
	}
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
		if route.ExposureMode == ingress.CloudflareTunnel {
			var desiredState string
			if err = a.db.QueryRowContext(r.Context(), `SELECT desired_state FROM exposure_providers WHERE provider_id=? AND provider_type='cloudflare-tunnel'`, route.ProviderID).Scan(&desiredState); err != nil || desiredState != "enabled" {
				http.Error(w, "Cloudflare provider must be configured and enabled", http.StatusBadRequest)
				return
			}
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
			if route.CertificateID != "" {
				_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='active',updated_at=? WHERE certificate_id=? AND state IN ('issued','active')`, time.Now().UTC().Format(time.RFC3339Nano), route.CertificateID)
			}
			if route.ExposureMode == ingress.CloudflareTunnel {
				if err = a.addCloudflareRemoteRoute(r.Context(), route.ProviderID, route.Hostname); err != nil {
					_, _ = a.db.ExecContext(r.Context(), `DELETE FROM ingress_routes WHERE route_id=?`, route.ID)
					if _, rollbackErr := a.applyIngress(r, store); rollbackErr != nil {
						http.Error(w, "Cloudflare route failed and local ingress recovery requires attention", http.StatusServiceUnavailable)
						return
					}
					http.Error(w, "Cloudflare route failed; local ingress was rolled back", http.StatusBadGateway)
					return
				}
			}
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
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM ingress_route_holds WHERE route_id=?`, routeID)
		writeJSON(w, http.StatusOK, route)
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
	}
}

func (a *app) addCloudflareRemoteRoute(ctx context.Context, providerID, hostname string) error {
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "AddCloudflareRoute", OperationRevision: 1, ProviderID: providerID, Hostname: hostname})
	if err != nil || !response.OK {
		return errors.New("Cloudflare route rejected")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = a.db.ExecContext(ctx, `INSERT INTO exposure_provider_routes(provider_id,hostname,remote_state,last_error_code,created_at,updated_at) VALUES(?,?,'configured','',?,?) ON CONFLICT(provider_id,hostname) DO UPDATE SET remote_state='configured',last_error_code='',updated_at=excluded.updated_at`, providerID, hostname, now, now); err != nil {
		_, _ = a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RemoveCloudflareRemoteRoute", OperationRevision: 1, ProviderID: providerID, Hostname: hostname, ConfirmRemote: true})
		return err
	}
	return nil
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
		var held int
		if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_route_holds WHERE route_id=?`, route.ID).Scan(&held); err != nil {
			return 0, err
		}
		if held > 0 {
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
	if err = store.RecordGeneration(r.Context(), candidate, operationID, "validated", ""); err != nil {
		return 0, err
	}
	if err = store.CommitGeneration(r.Context(), candidate, activeIDs); err != nil {
		return 0, err
	}
	return result.Generation, nil
}

func (a *app) quiesceIngressForRestore(r *http.Request, installationID string) ([]ingress.Route, error) {
	store := ingress.Store{DB: a.db}
	routes, err := store.ListRoutes(r.Context(), installationID)
	if err != nil {
		return nil, err
	}
	enabled := make([]ingress.Route, 0, len(routes))
	for _, route := range routes {
		if route.DesiredState == ingress.Enabled {
			enabled = append(enabled, route)
		}
	}
	if len(enabled) == 0 {
		return enabled, nil
	}
	if err = setRouteHolds(r.Context(), a.db, enabled, true); err != nil {
		return nil, err
	}
	if _, err = a.applyIngress(r, store); err == nil {
		return enabled, nil
	}
	_ = setRouteHolds(r.Context(), a.db, enabled, false)
	if _, recoveryErr := a.applyIngress(r, store); recoveryErr != nil {
		return nil, fmt.Errorf("quiesce failed and working ingress recovery failed")
	}
	return nil, fmt.Errorf("quiesce ingress generation failed")
}

func (a *app) reactivateIngressAfterRestore(r *http.Request, routes []ingress.Route) error {
	for _, route := range routes {
		if a.observeRouteReadiness(r.Context(), route).State != "ready" {
			return fmt.Errorf("restored backend readiness failed")
		}
	}
	if err := setRouteHolds(r.Context(), a.db, routes, false); err != nil {
		return err
	}
	store := ingress.Store{DB: a.db}
	if _, err := a.applyIngress(r, store); err == nil {
		return nil
	}
	_ = setRouteHolds(r.Context(), a.db, routes, true)
	if _, recoveryErr := a.applyIngress(r, store); recoveryErr != nil {
		return fmt.Errorf("ingress activation failed and safe disabled generation requires recovery")
	}
	return fmt.Errorf("ingress activation failed; routes remain disabled")
}

func (a *app) observeRouteReadiness(ctx context.Context, route ingress.Route) readiness.Observation {
	observation := readiness.Observation{State: "failed", ReasonCode: "restore_binding_unavailable", Summary: "Managed application binding is unavailable", CheckedAt: time.Now().UTC()}
	var applicationID string
	if err := a.db.QueryRowContext(ctx, `SELECT application_id FROM installations WHERE installation_id=?`, route.InstallationID).Scan(&applicationID); err == nil {
		if entry, ok := a.catalog[applicationID]; ok {
			service, found := findService(entry.Manifest, route.ServiceID)
			binding, bindingErr := exposure.Get(ctx, a.db, route.InstallationID, route.ServiceID)
			if found && bindingErr == nil {
				if a.probeReadiness != nil {
					observation = a.probeReadiness(ctx, service.Readiness, binding.HostAddress, binding.HostPort)
				} else {
					observation = (readiness.Prober{}).Probe(ctx, service.Readiness, binding.HostAddress, binding.HostPort)
				}
			}
		}
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO readiness_observations(entity_type,entity_id,dimension,state,reason_code,summary,checked_at) VALUES('route',?,'application',?,?,?,?) ON CONFLICT(entity_type,entity_id,dimension) DO UPDATE SET state=excluded.state,reason_code=excluded.reason_code,summary=excluded.summary,checked_at=excluded.checked_at`, route.ID, observation.State, observation.ReasonCode, observation.Summary, observation.CheckedAt.Format(time.RFC3339Nano))
	return observation
}

func setRouteHolds(ctx context.Context, db *sql.DB, routes []ingress.Route, held bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, route := range routes {
		if held {
			if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO ingress_route_holds(route_id,reason,created_at) VALUES(?,'application-restore',?)`, route.ID, now); err != nil {
				return err
			}
		} else if _, err = tx.ExecContext(ctx, `DELETE FROM ingress_route_holds WHERE route_id=? AND reason='application-restore'`, route.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type certificateImport struct {
	Method         string `json:"method"`
	Hostname       string `json:"hostname"`
	CertificatePEM string `json:"certificate_pem"`
	PrivateKeyPEM  string `json:"private_key_pem"`
	Email          string `json:"email"`
	Directory      string `json:"directory"`
	AcceptTerms    bool   `json:"accept_terms"`
	InstallationID string `json:"installation_id"`
	ServiceID      string `json:"service_id"`
}

func (a *app) certificates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		now := time.Now().UTC()
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='invalid',last_error_code='expired',updated_at=? WHERE not_after<>'' AND not_after<=? AND state<>'revoked'`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339))
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='expiring',updated_at=? WHERE not_after<>'' AND not_after>? AND not_after<=? AND state IN ('issued','active')`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339), now.Add(30*24*time.Hour).Format(time.RFC3339))
		rows, err := a.db.QueryContext(r.Context(), `SELECT c.certificate_id,c.hostname,c.method,c.state,c.leaf_sha256,c.issuer,c.not_before,c.not_after,c.last_renewal_at,c.last_error_code,c.created_at,c.updated_at,COALESCE(a.next_renewal_at,'') FROM certificates c LEFT JOIN certificate_acme a ON a.certificate_id=c.certificate_id ORDER BY c.hostname`)
		if err != nil {
			http.Error(w, "certificate state unavailable", 500)
			return
		}
		defer rows.Close()
		var values []map[string]string
		for rows.Next() {
			var id, hostname, method, state, digest, issuer, before, after, renewal, errorCode, created, updated, nextRenewal string
			if rows.Scan(&id, &hostname, &method, &state, &digest, &issuer, &before, &after, &renewal, &errorCode, &created, &updated, &nextRenewal) != nil {
				http.Error(w, "certificate state unavailable", 500)
				return
			}
			values = append(values, map[string]string{"certificate_id": id, "hostname": hostname, "method": method, "state": state, "leaf_sha256": digest, "issuer": issuer, "not_before": before, "not_after": after, "last_renewal_at": renewal, "next_renewal_at": nextRenewal, "last_error_code": errorCode, "created_at": created, "updated_at": updated})
		}
		writeJSON(w, http.StatusOK, map[string]any{"certificates": values})
	case http.MethodPost:
		if strings.HasSuffix(r.URL.Path, "/renew") {
			a.renewCertificate(w, r)
			return
		}
		if r.URL.Path != "/api/v1/certificates" {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}
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
		method := input.Method
		if method == "" {
			method = "provided"
		}
		if method != "provided" && method != "acme" {
			http.Error(w, "invalid certificate method", 400)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		operation := "InstallCertificateMaterial"
		request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: operation, OperationRevision: 1, CertificateID: certificateID, Hostname: hostname, CertificatePEM: input.CertificatePEM, PrivateKeyPEM: input.PrivateKeyPEM}
		if method == "acme" {
			allowedModes, external, grantErr := a.ingressGrant(r, input.InstallationID, input.ServiceID)
			managedHTTPSAllowed := false
			for _, mode := range allowedModes {
				managedHTTPSAllowed = managedHTTPSAllowed || mode == string(ingress.ManagedHTTPS)
			}
			if grantErr != nil || !external || !managedHTTPSAllowed {
				http.Error(w, "hostname is not authorized for a managed ingress service", http.StatusBadRequest)
				return
			}
			var hostnameClaimed int
			if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_routes WHERE hostname=?`, hostname).Scan(&hostnameClaimed); err != nil || hostnameClaimed != 0 {
				http.Error(w, "hostname is already assigned", http.StatusConflict)
				return
			}
			request.Operation = "IssueACMECertificate"
			request.ACMEEmail, request.ACMEDirectory, request.AcceptACMETerms = input.Email, input.Directory, input.AcceptTerms
			request.CertificatePEM, request.PrivateKeyPEM = "", ""
			if _, err = a.db.ExecContext(r.Context(), `INSERT INTO certificates(certificate_id,hostname,method,state,created_at,updated_at) VALUES(?,?,'acme','requested',?,?)`, certificateID, hostname, now, now); err != nil {
				http.Error(w, "certificate request could not be recorded", 500)
				return
			}
		}
		response, err := a.callHelperOperation(request)
		input.PrivateKeyPEM, input.CertificatePEM = "", ""
		if err != nil || !response.OK {
			if method == "acme" {
				_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='invalid',last_error_code='issuance_failed',updated_at=? WHERE certificate_id=?`, time.Now().UTC().Format(time.RFC3339Nano), certificateID)
			}
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
			if method == "acme" {
				_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='invalid',last_error_code='invalid_helper_result',updated_at=? WHERE certificate_id=?`, time.Now().UTC().Format(time.RFC3339Nano), certificateID)
			}
			http.Error(w, "certificate helper returned invalid metadata", 502)
			return
		}
		now = time.Now().UTC().Format(time.RFC3339Nano)
		if method == "provided" {
			_, err = a.db.ExecContext(r.Context(), `INSERT INTO certificates(certificate_id,hostname,method,state,leaf_sha256,issuer,not_before,not_after,last_renewal_at,last_error_code,created_at,updated_at) VALUES(?,?,'provided','issued',?,'',?,?,'','',?,?)`, certificateID, hostname, metadata.CertificateSHA256, metadata.NotBefore.UTC().Format(time.RFC3339), metadata.NotAfter.UTC().Format(time.RFC3339), now, now)
		} else {
			renewAt := metadata.NotAfter.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
			tx, txErr := a.db.BeginTx(r.Context(), nil)
			if txErr != nil {
				err = txErr
			} else {
				defer tx.Rollback()
				_, err = tx.ExecContext(r.Context(), `UPDATE certificates SET state='issued',leaf_sha256=?,not_before=?,not_after=?,last_error_code='',updated_at=? WHERE certificate_id=?`, metadata.CertificateSHA256, metadata.NotBefore.UTC().Format(time.RFC3339), metadata.NotAfter.UTC().Format(time.RFC3339), now, certificateID)
				if err == nil {
					_, err = tx.ExecContext(r.Context(), `INSERT INTO certificate_acme(certificate_id,contact_email,directory,next_renewal_at,last_attempt_at,last_success_at,last_error_code) VALUES(?,?,?,?,?,?, '')`, certificateID, input.Email, input.Directory, renewAt, now, now)
				}
				if err == nil {
					err = tx.Commit()
				}
			}
		}
		if err != nil {
			http.Error(w, "certificate metadata could not be committed", 500)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"certificate_id": certificateID, "hostname": hostname, "method": method, "state": "issued", "not_before": metadata.NotBefore, "not_after": metadata.NotAfter})
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
	}
}

func (a *app) renewCertificate(w http.ResponseWriter, r *http.Request) {
	certificateID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/certificates/"), "/renew")
	if !strings.HasPrefix(certificateID, "cert-") || len(certificateID) != 21 {
		http.Error(w, "certificate not found", http.StatusNotFound)
		return
	}
	var hostname, method string
	if err := a.db.QueryRowContext(r.Context(), `SELECT hostname,method FROM certificates WHERE certificate_id=?`, certificateID).Scan(&hostname, &method); err != nil || method != "acme" {
		http.Error(w, "certificate not found", http.StatusNotFound)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := a.db.ExecContext(r.Context(), `UPDATE certificates SET state='renewal-pending',last_error_code='',updated_at=? WHERE certificate_id=? AND state<>'renewal-pending'`, now, certificateID)
	if err != nil {
		http.Error(w, "certificate state unavailable", 500)
		return
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		http.Error(w, "certificate renewal already in progress", http.StatusConflict)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE certificate_acme SET last_attempt_at=?,last_error_code='' WHERE certificate_id=?`, now, certificateID)
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RenewACMECertificate", OperationRevision: 1, CertificateID: certificateID, Hostname: hostname})
	if err != nil || !response.OK {
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='renewal-failed',last_error_code='renewal_failed',updated_at=? WHERE certificate_id=?`, time.Now().UTC().Format(time.RFC3339Nano), certificateID)
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificate_acme SET last_error_code='renewal_failed' WHERE certificate_id=?`, certificateID)
		http.Error(w, "certificate renewal failed; previous certificate retained", http.StatusBadGateway)
		return
	}
	var metadata struct {
		CertificateSHA256 string    `json:"certificate_sha256"`
		NotBefore         time.Time `json:"not_before"`
		NotAfter          time.Time `json:"not_after"`
	}
	encoded, _ := json.Marshal(response.Result)
	if json.Unmarshal(encoded, &metadata) != nil || len(metadata.CertificateSHA256) != 64 {
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='renewal-failed',last_error_code='invalid_helper_result',updated_at=? WHERE certificate_id=?`, time.Now().UTC().Format(time.RFC3339Nano), certificateID)
		_, _ = a.db.ExecContext(r.Context(), `UPDATE certificate_acme SET last_error_code='invalid_helper_result' WHERE certificate_id=?`, certificateID)
		http.Error(w, "certificate helper returned invalid metadata", http.StatusBadGateway)
		return
	}
	now = time.Now().UTC().Format(time.RFC3339Nano)
	renewAt := metadata.NotAfter.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err == nil {
		defer tx.Rollback()
		_, err = tx.ExecContext(r.Context(), `UPDATE certificates SET state='active',leaf_sha256=?,not_before=?,not_after=?,last_renewal_at=?,last_error_code='',updated_at=? WHERE certificate_id=?`, metadata.CertificateSHA256, metadata.NotBefore.UTC().Format(time.RFC3339), metadata.NotAfter.UTC().Format(time.RFC3339), now, now, certificateID)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE certificate_acme SET next_renewal_at=?,last_success_at=?,last_error_code='' WHERE certificate_id=?`, renewAt, now, certificateID)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		http.Error(w, "certificate renewal state could not be committed", 500)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificate_id": certificateID, "hostname": hostname, "method": "acme", "state": "active", "not_after": metadata.NotAfter, "next_renewal_at": renewAt})
}

type renewalResponseSink struct{ header http.Header }

func (s *renewalResponseSink) Header() http.Header {
	if s.header == nil {
		s.header = make(http.Header)
	}
	return s.header
}
func (*renewalResponseSink) Write(value []byte) (int, error) { return len(value), nil }
func (*renewalResponseSink) WriteHeader(int)                 {}

func (a *app) runACMERenewalScheduler() {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		<-timer.C
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		a.renewDueCertificates(ctx)
		cancel()
		timer.Reset(12 * time.Hour)
	}
}

func (a *app) renewDueCertificates(ctx context.Context) {
	rows, err := a.db.QueryContext(ctx, `SELECT c.certificate_id,c.hostname FROM certificates c JOIN certificate_acme a ON a.certificate_id=c.certificate_id WHERE a.next_renewal_at<=? AND c.state NOT IN ('renewal-pending','revoked','invalid') ORDER BY a.next_renewal_at LIMIT 16`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	var due [][2]string
	for rows.Next() {
		var value [2]string
		if rows.Scan(&value[0], &value[1]) == nil {
			due = append(due, value)
		}
	}
	rows.Close()
	for _, value := range due {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/certificates/"+value[0]+"/renew", nil)
		if err != nil {
			continue
		}
		a.renewCertificate(&renewalResponseSink{}, request)
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

type providerInput struct {
	ProviderID    string `json:"provider_id"`
	AccountID     string `json:"account_id"`
	ZoneID        string `json:"zone_id"`
	TunnelID      string `json:"tunnel_id"`
	APIToken      string `json:"api_token"`
	Hostname      string `json:"hostname"`
	ConfirmRemote bool   `json:"confirm_remote"`
}

func (a *app) exposureProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/exposure-providers" {
		rows, err := a.db.QueryContext(r.Context(), `SELECT provider_id,provider_type,account_id,zone_id,tunnel_id,desired_state,runtime_state,last_error_code,created_at,updated_at FROM exposure_providers ORDER BY provider_id`)
		if err != nil {
			http.Error(w, "provider state unavailable", 500)
			return
		}
		defer rows.Close()
		providers := []map[string]string{}
		for rows.Next() {
			var id, providerType, account, zone, tunnel, desired, runtimeState, errorCode, created, updated string
			if rows.Scan(&id, &providerType, &account, &zone, &tunnel, &desired, &runtimeState, &errorCode, &created, &updated) != nil {
				http.Error(w, "provider state unavailable", 500)
				return
			}
			providers = append(providers, map[string]string{"provider_id": id, "provider_type": providerType, "account_id": account, "zone_id": zone, "tunnel_id": tunnel, "desired_state": desired, "runtime_state": runtimeState, "last_error_code": errorCode, "created_at": created, "updated_at": updated})
		}
		writeJSON(w, 200, map[string]any{"providers": providers})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/exposure-providers" {
		var input providerInput
		if decodeStrictJSON(r, 32*1024, &input) != nil {
			http.Error(w, "invalid provider request", 400)
			return
		}
		providerID := input.ProviderID
		if providerID == "" {
			var count int
			if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM exposure_providers WHERE desired_state<>'unlinked'`).Scan(&count); err != nil || count != 0 {
				http.Error(w, "a Cloudflare provider is already configured", http.StatusConflict)
				return
			}
			providerID, _ = ingress.NewID("provider")
		} else {
			var account, zone, tunnel string
			if err := a.db.QueryRowContext(r.Context(), `SELECT account_id,zone_id,tunnel_id FROM exposure_providers WHERE provider_id=?`, providerID).Scan(&account, &zone, &tunnel); err != nil {
				http.Error(w, "provider not found", http.StatusNotFound)
				return
			}
			if account != input.AccountID || zone != input.ZoneID || tunnel != input.TunnelID {
				http.Error(w, "credential replacement cannot change remote resource identity", 400)
				return
			}
		}
		digest := sha256.Sum256([]byte(input.APIToken))
		response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "ConfigureCloudflareProvider", OperationRevision: 1, ProviderID: providerID, ProviderAccount: input.AccountID, ProviderZone: input.ZoneID, ProviderTunnel: input.TunnelID, ProviderToken: input.APIToken})
		input.APIToken = ""
		if err != nil || !response.OK {
			http.Error(w, "Cloudflare credential or permissions were rejected", http.StatusBadGateway)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = a.db.ExecContext(r.Context(), `INSERT INTO exposure_providers(provider_id,provider_type,account_id,zone_id,tunnel_id,credential_sha256,desired_state,runtime_state,last_error_code,created_at,updated_at) VALUES(?,'cloudflare-tunnel',?,?,?,?, 'disabled','offline','',?,?) ON CONFLICT(provider_id) DO UPDATE SET credential_sha256=excluded.credential_sha256,last_error_code='',updated_at=excluded.updated_at`, providerID, input.AccountID, input.ZoneID, input.TunnelID, hex.EncodeToString(digest[:]), now, now)
		if err != nil {
			http.Error(w, "provider state could not be committed", 500)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"provider_id": providerID, "provider_type": "cloudflare-tunnel", "desired_state": "disabled", "runtime_state": "offline"})
		return
	}
	a.providerAction(w, r)
}

func (a *app) providerAction(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/exposure-providers/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "provider-") || len(parts[0]) != 25 {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	providerID, action := parts[0], parts[1]
	if r.Method == http.MethodGet && action == "health" && len(parts) == 2 {
		response, err := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), Operation: "InspectCloudflareProvider", ProviderID: providerID})
		if err != nil {
			http.Error(w, "provider health unavailable", http.StatusBadGateway)
			return
		}
		var result map[string]any
		encoded, _ := json.Marshal(response.Result)
		_ = json.Unmarshal(encoded, &result)
		if result == nil {
			result = map[string]any{"provider_id": providerID, "runtime_state": "failed"}
		}
		errorCode := ""
		if !response.OK {
			errorCode = response.ErrorCode
		}
		if runtimeState, ok := result["runtime_state"].(string); ok {
			_, _ = a.db.ExecContext(r.Context(), `UPDATE exposure_providers SET runtime_state=?,last_error_code=?,updated_at=? WHERE provider_id=?`, runtimeState, errorCode, time.Now().UTC().Format(time.RFC3339Nano), providerID)
		}
		result["last_error_code"] = errorCode
		writeJSON(w, 200, result)
		return
	}
	if r.Method == http.MethodDelete && len(parts) == 2 && action == "unlink" {
		a.runProviderMutation(w, r, "UnlinkCloudflareProvider", providerID, "", false)
		return
	}
	if r.Method == http.MethodDelete && len(parts) == 2 && action == "remote-tunnel" {
		a.runProviderMutation(w, r, "DeleteCloudflareRemoteTunnel", providerID, "", r.URL.Query().Get("confirm_remote") == "true")
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && (action == "enable" || action == "disable") {
		operation := "EnableCloudflareProvider"
		if action == "disable" {
			operation = "DisableCloudflareProvider"
		}
		a.runProviderMutation(w, r, operation, providerID, "", false)
		return
	}
	if len(parts) == 3 && action == "routes" && r.Method == http.MethodDelete {
		hostname, err := ingress.NormalizeHostname(parts[2])
		if err != nil || hostname != parts[2] {
			http.Error(w, "invalid hostname", 400)
			return
		}
		a.runProviderMutation(w, r, "RemoveCloudflareRemoteRoute", providerID, hostname, r.URL.Query().Get("confirm_remote") == "true")
		return
	}
	http.Error(w, "route not found", http.StatusNotFound)
}

func (a *app) runProviderMutation(w http.ResponseWriter, r *http.Request, operation, providerID, hostname string, confirmRemote bool) bool {
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: operation, OperationRevision: 1, ProviderID: providerID, Hostname: hostname, ConfirmRemote: confirmRemote})
	if err != nil || !response.OK {
		http.Error(w, "Cloudflare provider operation failed", http.StatusBadGateway)
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch operation {
	case "EnableCloudflareProvider":
		_, err = a.db.ExecContext(r.Context(), `UPDATE exposure_providers SET desired_state='enabled',runtime_state='connecting',last_error_code='',updated_at=? WHERE provider_id=?`, now, providerID)
	case "DisableCloudflareProvider":
		_, err = a.db.ExecContext(r.Context(), `UPDATE exposure_providers SET desired_state='disabled',runtime_state='offline',last_error_code='',updated_at=? WHERE provider_id=?`, now, providerID)
	case "RemoveCloudflareRemoteRoute":
		_, err = a.db.ExecContext(r.Context(), `UPDATE exposure_provider_routes SET remote_state='removed',last_error_code='',updated_at=? WHERE provider_id=? AND hostname=?`, now, providerID, hostname)
	case "UnlinkCloudflareProvider", "DeleteCloudflareRemoteTunnel":
		_, err = a.db.ExecContext(r.Context(), `DELETE FROM exposure_providers WHERE provider_id=?`, providerID)
	}
	if err != nil {
		http.Error(w, "provider state could not be committed", 500)
		return false
	}
	writeJSON(w, 200, map[string]any{"provider_id": providerID, "operation": operation, "hostname": hostname})
	return true
}

type setupGuideStep struct {
	ID, Title, Description, State, StateLabel, Summary string
	Number                                             int
}

func (a *app) firstRun(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid setup action", http.StatusBadRequest)
			return
		}
		step, choice := r.FormValue("step_id"), r.FormValue("choice")
		switch {
		case step == "access-mode" && (choice == "local" || choice == "lan"):
			stateValue, reason := "complete", "access_mode_confirmed"
			if !a.accessModeReady(choice) {
				stateValue, reason = "attention", "listener_configuration_required"
			}
			if err := a.saveSetupStep(r.Context(), step, stateValue, map[string]any{"mode": choice, "reason_code": reason}); err != nil {
				http.Error(w, "setup state unavailable", http.StatusInternalServerError)
				return
			}
		case (step == "https" || step == "cloudflare") && choice == "skip":
			if err := a.saveSetupStep(r.Context(), step, "skipped", map[string]any{"reason_code": "operator_deferred"}); err != nil {
				http.Error(w, "setup state unavailable", http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, "unsupported setup action", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/first-run", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	a.refreshSetupState(r.Context())
	titles := map[string]string{"host-readiness": "Host readiness", "runtime": "Container runtime", "storage": "Storage", "network": "Local network", "docker-address-pool": "Docker address pool", "administrator": "Administrator", "access-mode": "Local or LAN access", "https": "Optional HTTPS", "cloudflare": "Optional Cloudflare", "first-application": "First application"}
	descriptions := map[string]string{
		"host-readiness":      "Checks the protected helper and base host integration.",
		"runtime":             "Confirms the supported container runtime is responding.",
		"storage":             "Registers an administrator-approved location for application data.",
		"network":             "Detects a private IPv4 address without changing host networking.",
		"docker-address-pool": "Checks capacity and conflicts. KITPro never rewrites Docker daemon configuration.",
		"administrator":       "Uses the local administrator created during secure bootstrap.",
		"access-mode":         "Records local-only or LAN intent and verifies the current control-service listener.",
		"https":               "HTTPS is optional during setup and can use a provided or ACME certificate later.",
		"cloudflare":          "Cloudflare Tunnel is optional. Local access remains independent.",
		"first-application":   "Installs the first trusted catalog application through the normal lifecycle.",
	}
	steps := make([]setupGuideStep, 0, len(setupStepOrder))
	completed := 0
	for index, id := range setupStepOrder {
		var stateValue, evidence string
		if err := a.db.QueryRowContext(r.Context(), `SELECT state,evidence_json FROM setup_steps WHERE step_id=?`, id).Scan(&stateValue, &evidence); err != nil {
			continue
		}
		var facts map[string]any
		_ = json.Unmarshal([]byte(evidence), &facts)
		reason, _ := facts["reason_code"].(string)
		summary := setupReasonSummary(reason)
		if stateValue == "complete" || stateValue == "skipped" {
			completed++
		}
		steps = append(steps, setupGuideStep{ID: id, Number: index + 1, Title: titles[id], Description: descriptions[id], State: stateValue, StateLabel: strings.Title(strings.ReplaceAll(stateValue, "-", " ")), Summary: summary})
	}
	csrf := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
		csrf = cookie.Value
	}
	a.securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.setupTmpl.Execute(w, map[string]any{"Steps": steps, "Completed": completed, "Total": len(setupStepOrder), "Complete": completed == len(setupStepOrder), "CSRF": csrf}); err != nil {
		slog.Default().Error("first-run page render failed", "event", "ui_render_failed", "error", err)
	}
}

func (a *app) accessModeReady(choice string) bool {
	host, _, err := net.SplitHostPort(a.listenAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if choice == "local" {
		return host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	if host != "" && host != "0.0.0.0" && (ip == nil || !ip.IsPrivate()) {
		return false
	}
	for allowed := range a.allowedHosts {
		allowedHost, _, splitErr := net.SplitHostPort(allowed)
		allowedIP := net.ParseIP(allowedHost)
		if splitErr == nil && allowedHost != "localhost" {
			if allowedIP == nil || (allowedIP.IsPrivate() && !allowedIP.IsLoopback()) {
				return true
			}
		}
	}
	return false
}

func (a *app) saveSetupStep(ctx context.Context, step, stateValue string, facts map[string]any) error {
	evidence, err := json.Marshal(facts)
	if err != nil || len(evidence) > 8192 || diagnosticsContainsSensitiveKey(facts) {
		return fmt.Errorf("unsafe setup evidence")
	}
	digest := sha256.Sum256(evidence)
	_, err = a.db.ExecContext(ctx, `UPDATE setup_steps SET state=?,input_sha256=?,evidence_json=?,updated_at=? WHERE step_id=?`, stateValue, hex.EncodeToString(digest[:]), string(evidence), time.Now().UTC().Format(time.RFC3339Nano), step)
	return err
}

func setupReasonSummary(reason string) string {
	if summary, ok := map[string]string{
		"helper_ready":                    "Protected helper is ready.",
		"helper_unavailable":              "Protected helper requires attention.",
		"runtime_ready":                   "Container runtime is ready.",
		"runtime_unavailable":             "Container runtime requires attention.",
		"address_pool_ready":              "Docker network allocation is ready.",
		"address_pool_attention":          "Docker address-pool configuration requires attention and may require a Docker restart.",
		"storage_registered":              "Trusted storage is registered.",
		"storage_not_registered":          "Register a trusted storage location to continue.",
		"lan_address_detected":            "A private LAN address is available.",
		"lan_address_unavailable":         "No private IPv4 LAN address was detected.",
		"administrator_configured":        "Local administrator is configured.",
		"access_mode_confirmed":           "The selected access mode matches the active listener.",
		"listener_configuration_required": "The selected mode requires an explicit service-listener configuration change and restart.",
		"certificate_configured":          "HTTPS certificate is configured.",
		"provider_configured":             "Cloudflare provider is configured.",
		"application_installed":           "An application is installed.",
		"application_not_installed":       "Choose and install the first application.",
		"operator_deferred":               "Deferred by the administrator; this can be configured later.",
	}[reason]; ok {
		return summary
	}
	return "Not completed yet."
}

func (a *app) setupState(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var input struct {
			StepID   string         `json:"step_id"`
			State    string         `json:"state"`
			Evidence map[string]any `json:"evidence"`
		}
		if decodeStrictJSON(r, 16*1024, &input) != nil {
			http.Error(w, "invalid setup update", 400)
			return
		}
		manual := input.StepID == "access-mode" && input.State == "complete"
		optional := (input.StepID == "https" || input.StepID == "cloudflare") && input.State == "skipped"
		if !manual && !optional {
			http.Error(w, "invalid setup update", 400)
			return
		}
		if manual {
			mode, _ := input.Evidence["mode"].(string)
			if mode != "local" && mode != "lan" {
				http.Error(w, "invalid access mode", 400)
				return
			}
			input.Evidence = map[string]any{"mode": mode, "reason_code": "access_mode_confirmed"}
			if !a.accessModeReady(mode) {
				input.State = "attention"
				input.Evidence["reason_code"] = "listener_configuration_required"
			}
		} else {
			input.Evidence = map[string]any{"reason_code": "operator_deferred"}
		}
		if err := a.saveSetupStep(r.Context(), input.StepID, input.State, input.Evidence); err != nil {
			http.Error(w, "setup state unavailable", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"step_id": input.StepID, "state": input.State})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	a.refreshSetupState(r.Context())
	rows, err := a.db.QueryContext(r.Context(), `SELECT step_id,state,evidence_json,updated_at FROM setup_steps ORDER BY CASE step_id WHEN 'host-readiness' THEN 1 WHEN 'runtime' THEN 2 WHEN 'storage' THEN 3 WHEN 'network' THEN 4 WHEN 'docker-address-pool' THEN 5 WHEN 'administrator' THEN 6 WHEN 'access-mode' THEN 7 WHEN 'https' THEN 8 WHEN 'cloudflare' THEN 9 WHEN 'first-application' THEN 10 ELSE 99 END`)
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

func (a *app) refreshSetupState(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	update := func(step, stateValue, reason string) {
		evidence, _ := json.Marshal(map[string]string{"reason_code": reason})
		digest := sha256.Sum256(evidence)
		_, _ = a.db.ExecContext(ctx, `UPDATE setup_steps SET state=?,input_sha256=?,evidence_json=?,updated_at=? WHERE step_id=?`, stateValue, hex.EncodeToString(digest[:]), string(evidence), now, step)
	}
	helper, helperErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "Ping"})
	helperReady := helperErr == nil && helper.OK
	if helperReady {
		update("host-readiness", "complete", "helper_ready")
	} else {
		update("host-readiness", "attention", "helper_unavailable")
	}
	runtimeResponse, runtimeErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"})
	if runtimeErr == nil && runtimeResponse.OK {
		update("runtime", "complete", "runtime_ready")
	} else {
		update("runtime", "attention", "runtime_unavailable")
	}
	poolResponse, poolErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectDockerAddressPool"})
	poolState := ""
	if encoded, err := json.Marshal(poolResponse.Result); err == nil {
		var pool map[string]any
		_ = json.Unmarshal(encoded, &pool)
		poolState, _ = pool["state"].(string)
	}
	if poolErr == nil && poolResponse.OK && (poolState == "ready" || poolState == "not-applicable") {
		update("docker-address-pool", "complete", "address_pool_ready")
	} else {
		update("docker-address-pool", "attention", "address_pool_attention")
	}
	storage, storageErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "ListStorageRoots"})
	if storageErr == nil && storage.OK && resultHasItems(storage.Result) {
		update("storage", "complete", "storage_registered")
	} else {
		update("storage", "attention", "storage_not_registered")
	}
	if address := firstLANAddress(); address != "" {
		update("network", "complete", "lan_address_detected")
	} else {
		update("network", "attention", "lan_address_unavailable")
	}
	if auth.HasAdmin(ctx, a.db) {
		update("administrator", "complete", "administrator_configured")
	}
	var count int
	if a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificates WHERE state IN ('issued','active','expiring','renewal-pending')`).Scan(&count) == nil && count > 0 {
		update("https", "complete", "certificate_configured")
	}
	if a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM exposure_providers WHERE desired_state IN ('disabled','enabled')`).Scan(&count) == nil && count > 0 {
		update("cloudflare", "complete", "provider_configured")
	}
	if a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM installations`).Scan(&count) == nil && count > 0 {
		update("first-application", "complete", "application_installed")
	} else {
		update("first-application", "pending", "application_not_installed")
	}
}

func resultHasItems(value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var list []any
	if json.Unmarshal(encoded, &list) == nil {
		return len(list) > 0
	}
	var object map[string]any
	if json.Unmarshal(encoded, &object) == nil {
		for _, key := range []string{"roots", "items"} {
			if items, ok := object[key].([]any); ok && len(items) > 0 {
				return true
			}
		}
	}
	return false
}

func firstLANAddress() string {
	interfaces, _ := net.Interfaces()
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(networkInterface.Name)
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "cni") || strings.HasPrefix(name, "podman") {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			host, _, _ := net.ParseCIDR(address.String())
			if host != nil && host.To4() != nil && host.IsPrivate() {
				return host.String()
			}
		}
	}
	return ""
}

var setupStepOrder = []string{"host-readiness", "runtime", "storage", "network", "docker-address-pool", "administrator", "access-mode", "https", "cloudflare", "first-application"}

func initializeSetupState(ctx context.Context, db *sql.DB, administratorComplete bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, step := range setupStepOrder {
		stateValue := "pending"
		if step == "administrator" && administratorComplete {
			stateValue = "complete"
		}
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO setup_steps(step_id,state,input_sha256,evidence_json,updated_at) VALUES(?,?,'','{}',?)`, step, stateValue, now); err != nil {
			return err
		}
	}
	return nil
}

func validSetupStep(value string) bool {
	for _, step := range setupStepOrder {
		if step == value {
			return true
		}
	}
	return false
}

func diagnosticsContainsSensitiveKey(value map[string]any) bool {
	for key, child := range value {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "credential") || strings.Contains(lower, "cookie") || strings.Contains(lower, "session") || strings.Contains(lower, "key") {
			return true
		}
		if nested, ok := child.(map[string]any); ok && diagnosticsContainsSensitiveKey(nested) {
			return true
		}
	}
	return false
}

func (a *app) systemReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", 405)
		return
	}
	type item struct {
		Dimension  string `json:"dimension"`
		State      string `json:"state"`
		ReasonCode string `json:"reason_code"`
		Summary    string `json:"summary"`
		Details    any    `json:"details,omitempty"`
	}
	values := []item{}
	helper, helperErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "Ping"})
	if helperErr == nil && helper.OK {
		values = append(values, item{Dimension: "helper", State: "ready", ReasonCode: "helper_ready", Summary: "Privileged helper is responding"})
	} else {
		values = append(values, item{Dimension: "helper", State: "failed", ReasonCode: "helper_unavailable", Summary: "Privileged helper is unavailable"})
	}
	runtimeResponse, runtimeErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"})
	if runtimeErr == nil && runtimeResponse.OK {
		values = append(values, item{Dimension: "runtime", State: "ready", ReasonCode: "runtime_ready", Summary: "Container runtime is responding"})
	} else {
		values = append(values, item{Dimension: "runtime", State: "failed", ReasonCode: "runtime_unavailable", Summary: "Container runtime is unavailable"})
	}
	poolResponse, poolErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectDockerAddressPool"})
	pool := map[string]any{"state": "failed", "reason_code": "inspection_failed", "summary": "Docker address-pool readiness could not be inspected"}
	if poolErr == nil && poolResponse.OK {
		encoded, _ := json.Marshal(poolResponse.Result)
		_ = json.Unmarshal(encoded, &pool)
	}
	poolState, _ := pool["state"].(string)
	poolReason, _ := pool["reason_code"].(string)
	poolSummary, _ := pool["summary"].(string)
	values = append(values, item{Dimension: "docker-address-pool", State: poolState, ReasonCode: poolReason, Summary: poolSummary, Details: map[string]any{"current_pools": pool["current_pools"], "minimum_subnet_capacity": pool["minimum_subnet_capacity"], "requires_docker_restart": pool["requires_docker_restart"], "remediation": pool["remediation"]}})
	storageResponse, storageErr := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "ListStorageRoots"})
	storageReady := storageErr == nil && storageResponse.OK && resultHasItems(storageResponse.Result)
	values = append(values, item{Dimension: "storage", State: map[bool]string{true: "ready", false: "attention"}[storageReady], ReasonCode: map[bool]string{true: "storage_registered", false: "storage_not_registered"}[storageReady], Summary: map[bool]string{true: "At least one trusted storage location is registered", false: "No trusted storage location is registered"}[storageReady]})
	var filesystem unix.Statfs_t
	if err := unix.Statfs("/srv/kitpro", &filesystem); err != nil {
		values = append(values, item{Dimension: "disk-capacity", State: "failed", ReasonCode: "disk_unavailable", Summary: "KITPro storage capacity could not be inspected"})
	} else {
		available := uint64(filesystem.Bavail) * uint64(filesystem.Bsize)
		stateValue := "ready"
		if available < 5<<30 {
			stateValue = "attention"
		}
		values = append(values, item{Dimension: "disk-capacity", State: stateValue, ReasonCode: map[bool]string{true: "disk_capacity_low", false: "disk_capacity_available"}[stateValue == "attention"], Summary: fmt.Sprintf("%d GiB available for KITPro-managed storage", available>>30), Details: map[string]any{"available_bytes": available}})
	}
	lan := firstLANAddress()
	values = append(values, item{Dimension: "lan-address", State: map[bool]string{true: "ready", false: "attention"}[lan != ""], ReasonCode: map[bool]string{true: "lan_address_detected", false: "lan_address_unavailable"}[lan != ""], Summary: map[bool]string{true: "LAN address detected", false: "No private IPv4 LAN address was detected"}[lan != ""], Details: map[string]any{"address": lan}})
	values = append(values, item{Dimension: "firewall", State: "unknown", ReasonCode: "firewall_not_inspected", Summary: "Host firewall state is not available through the constrained helper"})
	var routes, held, certificates, certificateFailures, providers, providerFailures int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_routes r WHERE desired_state='enabled' AND NOT EXISTS (SELECT 1 FROM ingress_route_holds h WHERE h.route_id=r.route_id)`).Scan(&routes)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ingress_route_holds`).Scan(&held)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM certificates WHERE state IN ('issued','active','expiring','renewal-pending')`).Scan(&certificates)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM certificates WHERE state IN ('renewal-failed','invalid')`).Scan(&certificateFailures)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM exposure_providers WHERE desired_state='enabled'`).Scan(&providers)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM exposure_providers WHERE desired_state='enabled' AND runtime_state NOT IN ('ready','connecting')`).Scan(&providerFailures)
	ingressState, ingressReason, ingressSummary := "not-configured", "ingress_not_configured", "No managed routes are active"
	if held > 0 {
		ingressState, ingressReason, ingressSummary = "attention", "restore_hold_active", fmt.Sprintf("%d route(s) await restored backend readiness", held)
	} else if routes > 0 {
		ingressState, ingressReason, ingressSummary = "ready", "ingress_routes_active", fmt.Sprintf("%d managed route(s) active", routes)
	}
	values = append(values, item{Dimension: "ingress", State: ingressState, ReasonCode: ingressReason, Summary: ingressSummary})
	tlsState, tlsReason, tlsSummary := "not-configured", "tls_not_configured", "HTTPS is not configured"
	if certificateFailures > 0 {
		tlsState, tlsReason, tlsSummary = "attention", "certificate_attention", fmt.Sprintf("%d certificate(s) require attention", certificateFailures)
	} else if certificates > 0 {
		tlsState, tlsReason, tlsSummary = "ready", "certificates_available", fmt.Sprintf("%d certificate(s) available", certificates)
	}
	values = append(values, item{Dimension: "https", State: tlsState, ReasonCode: tlsReason, Summary: tlsSummary})
	providerState, providerReason, providerSummary := "disabled", "provider_disabled", "Remote access is disabled"
	if providerFailures > 0 {
		providerState, providerReason, providerSummary = "degraded", "provider_runtime_unavailable", "Cloudflare is enabled but its runtime requires attention"
	} else if providers > 0 {
		providerState, providerReason, providerSummary = "ready", "provider_enabled", "Cloudflare remote access is enabled"
	}
	values = append(values, item{Dimension: "cloudflare", State: providerState, ReasonCode: providerReason, Summary: providerSummary})
	writeJSON(w, 200, map[string]any{"readiness": values})
}

var _ = sql.ErrNoRows
