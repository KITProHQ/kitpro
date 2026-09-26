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
			if route.CertificateID != "" {
				_, _ = a.db.ExecContext(r.Context(), `UPDATE certificates SET state='active',updated_at=? WHERE certificate_id=? AND state IN ('issued','active')`, time.Now().UTC().Format(time.RFC3339Nano), route.CertificateID)
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

// The exposure-provider stub is replaced by the Cloudflare integration.
func (a *app) exposureProviders(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (a *app) setupState(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var input struct {
			StepID   string         `json:"step_id"`
			State    string         `json:"state"`
			Evidence map[string]any `json:"evidence"`
		}
		if decodeStrictJSON(r, 16*1024, &input) != nil || !validSetupStep(input.StepID) || (input.State != "complete" && input.State != "attention" && input.State != "skipped") {
			http.Error(w, "invalid setup update", 400)
			return
		}
		evidence, err := json.Marshal(input.Evidence)
		if err != nil || len(evidence) > 8192 || diagnosticsContainsSensitiveKey(input.Evidence) {
			http.Error(w, "unsafe setup evidence", 400)
			return
		}
		digest := sha256.Sum256(evidence)
		_, err = a.db.ExecContext(r.Context(), `INSERT INTO setup_steps(step_id,state,input_sha256,evidence_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(step_id) DO UPDATE SET state=excluded.state,input_sha256=excluded.input_sha256,evidence_json=excluded.evidence_json,updated_at=excluded.updated_at`, input.StepID, input.State, hex.EncodeToString(digest[:]), string(evidence), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
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
