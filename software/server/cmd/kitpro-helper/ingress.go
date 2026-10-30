package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	acmelifecycle "github.com/kitpro/kitpro/software/server/internal/acme"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/certificates"
	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

var ingressRuntime = func() ingress.Runtime { return ingress.NewNginxRuntime() }
var certificateStore = func() certificates.MaterialStore { return certificates.MaterialStore{} }
var acmeWorkerIssue = func(ctx context.Context, request acmelifecycle.WorkerRequest) (acmelifecycle.IssuedMaterial, error) {
	return (acmelifecycle.WorkerClient{}).Issue(ctx, request)
}
var reloadIngressCertificate = func(ctx context.Context) error {
	return ingress.NewNginxRuntime().ReloadAndVerifyCurrent(ctx)
}

func applyIngressGeneration(ctx context.Context, db *sql.DB, request protocol.Request) (map[string]any, error) {
	if len(request.IngressRoutes) > 128 {
		return nil, fmt.Errorf("too many ingress routes")
	}
	entries, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("trusted catalog unavailable")
	}
	applied := make([]ingress.AppliedRoute, 0, len(request.IngressRoutes))
	routeIDs := make([]string, 0, len(request.IngressRoutes))
	for _, candidate := range request.IngressRoutes {
		var applicationID string
		var generation int
		if err = db.QueryRowContext(ctx, `SELECT application_id,runtime_generation FROM runtime_generations WHERE installation_id=? AND status='active' ORDER BY runtime_generation DESC LIMIT 1`, candidate.InstallationID).Scan(&applicationID, &generation); err != nil {
			return nil, fmt.Errorf("route target is not an active managed installation")
		}
		entry, ok := entries[applicationID]
		if !ok {
			return nil, fmt.Errorf("route application is not trusted")
		}
		service, ok := findManifestService(entry.Manifest, candidate.ServiceID)
		if !ok || service.Ingress == nil {
			return nil, fmt.Errorf("service is not ingress eligible")
		}
		var address, mode, transport string
		var port, containerPort int
		if err = db.QueryRowContext(ctx, `SELECT host_address,host_port,mode,transport,container_port FROM runtime_generation_bindings WHERE installation_id=? AND runtime_generation=? AND service_id=?`, candidate.InstallationID, generation, candidate.ServiceID).Scan(&address, &port, &mode, &transport, &containerPort); err != nil {
			return nil, fmt.Errorf("route target binding is unavailable")
		}
		if mode != "loopback" || transport != "tcp" || address != "127.0.0.1" {
			return nil, fmt.Errorf("route target must be a managed loopback TCP binding")
		}
		route := ingress.Route{ID: candidate.RouteID, InstallationID: candidate.InstallationID, ServiceID: candidate.ServiceID, Hostname: candidate.Hostname, ExposureMode: ingress.ExposureMode(candidate.ExposureMode), UpstreamProtocol: candidate.UpstreamProtocol, TLSPolicy: ingress.TLSPolicy(candidate.TLSPolicy), CertificateID: candidate.CertificateID, DesiredState: ingress.Enabled}
		if err = ingress.ValidateRoute(route, service.Ingress.ExposureModes, service.Ingress.External); err != nil {
			return nil, err
		}
		applied = append(applied, ingress.AppliedRoute{Route: route, Backend: ingress.Backend{Address: address, Port: port}})
		routeIDs = append(routeIDs, route.ID)
	}
	configuration, err := ingress.RenderNginx(applied, ingress.RenderOptions{})
	if err != nil {
		return nil, err
	}
	var generation, previous int64
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM ingress_generations`).Scan(&generation); err != nil {
		return nil, err
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM ingress_generations WHERE status='active'`).Scan(&previous); err != nil {
		return nil, err
	}
	generation, err = ingress.NextUnusedGeneration("/var/lib/kitpro-helper/ingress", generation)
	if err != nil {
		return nil, err
	}
	manager := ingress.Manager{Runtime: ingressRuntime()}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	generationPath := filepath.Join("/var/lib/kitpro-helper/ingress/generations", fmt.Sprintf("%d", generation))
	if _, err = db.ExecContext(ctx, `INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,previous_generation,generation_path,created_at,failure_code,evidence_json) VALUES(?,?,?,'prepared',?,?,?,'','{}')`, generation, request.OperationID, "", nullableInt64(previous), generationPath, now); err != nil {
		return nil, err
	}
	result, err := manager.Apply(ctx, generation, previous, configuration)
	if err != nil {
		var applyError *ingress.ApplyError
		code := "apply_failed"
		status := "failed"
		if errors.As(err, &applyError) {
			code = applyError.Code
			if applyError.RecoveryNow {
				status = "rollback_pending"
			} else if applyError.RolledBack {
				status = "rolled_back"
			}
		}
		_, _ = db.ExecContext(ctx, `UPDATE ingress_generations SET configuration_sha256=?,status=?,failure_code=? WHERE generation=?`, result.SHA256, status, code, generation)
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if previous > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE ingress_generations SET status='rolled_back' WHERE generation=?`, previous); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ingress_generations SET configuration_sha256=?,status='active',validated_at=?,activated_at=? WHERE generation=?`, result.SHA256, now, now, generation); err != nil {
		return nil, err
	}
	for _, route := range applied {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ingress_generation_routes(generation,route_id,installation_id,service_id,hostname,backend_address,backend_port,exposure_mode,tls_policy,certificate_id,provider_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, generation, route.ID, route.InstallationID, route.ServiceID, route.Hostname, route.Backend.Address, route.Backend.Port, route.ExposureMode, route.TLSPolicy, route.CertificateID, route.ProviderID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"generation": generation, "previous_generation": previous, "configuration_sha256": result.SHA256, "route_ids": routeIDs}, nil
}

func findManifestService(value manifest.Manifest, serviceID string) (manifest.Service, bool) {
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

func installCertificateMaterial(ctx context.Context, db *sql.DB, request protocol.Request) (certificates.Metadata, error) {
	metadata, err := certificateStore().Install(request.CertificateID, request.Hostname, []byte(request.CertificatePEM), []byte(request.PrivateKeyPEM), time.Now().UTC())
	if err != nil {
		return certificates.Metadata{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.ExecContext(ctx, `INSERT INTO certificate_material(certificate_id,hostname,method,certificate_sha256,public_key_sha256,state,created_at,updated_at) VALUES(?,?,'provided',?,?,'issued',?,?) ON CONFLICT(certificate_id) DO UPDATE SET hostname=excluded.hostname,certificate_sha256=excluded.certificate_sha256,public_key_sha256=excluded.public_key_sha256,state='issued',updated_at=excluded.updated_at`, metadata.CertificateID, metadata.Hostname, metadata.CertificateSHA256, metadata.PublicKeySHA256, now, now)
	return metadata, err
}

func issueACMECertificate(ctx context.Context, db *sql.DB, request protocol.Request, renewal bool) (certificates.Metadata, error) {
	if request.CertificateID == "" || request.Hostname == "" {
		return certificates.Metadata{}, fmt.Errorf("invalid ACME request")
	}
	email, directory, acceptTerms := request.ACMEEmail, request.ACMEDirectory, request.AcceptACMETerms
	var existing int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificate_material WHERE certificate_id=? AND hostname=? AND method='acme'`, request.CertificateID, request.Hostname).Scan(&existing); err != nil {
		return certificates.Metadata{}, fmt.Errorf("certificate state unavailable")
	}
	if renewal {
		if existing != 1 {
			return certificates.Metadata{}, fmt.Errorf("ACME certificate is unavailable")
		}
		if err := db.QueryRowContext(ctx, `SELECT contact_email,directory FROM certificate_acme WHERE certificate_id=?`, request.CertificateID).Scan(&email, &directory); err != nil {
			return certificates.Metadata{}, fmt.Errorf("ACME renewal state unavailable")
		}
		acceptTerms = true
	} else if existing != 0 {
		return certificates.Metadata{}, fmt.Errorf("ACME certificate already exists")
	}
	material, err := acmeWorkerIssue(ctx, acmelifecycle.WorkerRequest{Hostname: request.Hostname, Email: email, Directory: directory, AcceptTerms: acceptTerms})
	if err != nil {
		return certificates.Metadata{}, err
	}
	materialStore := certificateStore()
	metadata, err := materialStore.Install(request.CertificateID, request.Hostname, material.CertificatePEM, material.PrivateKeyPEM, time.Now().UTC())
	material.PrivateKeyPEM, material.CertificatePEM = nil, nil
	if err != nil {
		return certificates.Metadata{}, fmt.Errorf("issued certificate material was rejected")
	}
	var referenced int
	if existing == 1 {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingress_generation_routes r JOIN ingress_generations g ON g.generation=r.generation WHERE r.certificate_id=? AND g.status='active'`, request.CertificateID).Scan(&referenced); err != nil {
			_ = materialStore.RollbackReplacement(request.CertificateID)
			return certificates.Metadata{}, fmt.Errorf("certificate route state unavailable")
		}
	}
	if referenced > 0 {
		if err = reloadIngressCertificate(ctx); err != nil {
			if rollbackErr := materialStore.RollbackReplacement(request.CertificateID); rollbackErr == nil {
				_ = reloadIngressCertificate(ctx)
			}
			return certificates.Metadata{}, fmt.Errorf("certificate activation failed; previous certificate retained")
		}
	}
	state := "issued"
	if referenced > 0 {
		state = "active"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	nextRenewal := metadata.NotAfter.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if existing == 1 {
			_ = materialStore.RollbackReplacement(request.CertificateID)
		}
		return certificates.Metadata{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO certificate_material(certificate_id,hostname,method,certificate_sha256,public_key_sha256,state,created_at,updated_at) VALUES(?,?,'acme',?,?,?, ?,?) ON CONFLICT(certificate_id) DO UPDATE SET certificate_sha256=excluded.certificate_sha256,public_key_sha256=excluded.public_key_sha256,state=excluded.state,updated_at=excluded.updated_at`, metadata.CertificateID, metadata.Hostname, metadata.CertificateSHA256, metadata.PublicKeySHA256, state, now, now); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO certificate_acme(certificate_id,contact_email,directory,next_renewal_at,last_attempt_at,last_success_at,last_error_code) VALUES(?,?,?,?,?,?, '') ON CONFLICT(certificate_id) DO UPDATE SET contact_email=excluded.contact_email,directory=excluded.directory,next_renewal_at=excluded.next_renewal_at,last_attempt_at=excluded.last_attempt_at,last_success_at=excluded.last_success_at,last_error_code=''`, metadata.CertificateID, email, directory, nextRenewal, now, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if existing == 1 {
			_ = materialStore.RollbackReplacement(request.CertificateID)
			if referenced > 0 {
				_ = reloadIngressCertificate(ctx)
			}
		}
		return certificates.Metadata{}, fmt.Errorf("certificate state could not be committed")
	}
	if existing == 1 {
		if err = materialStore.CommitReplacement(request.CertificateID); err != nil {
			return certificates.Metadata{}, fmt.Errorf("certificate cleanup failed")
		}
	}
	return metadata, nil
}

func acmeErrorCode(err error) string {
	var lifecycleError *acmelifecycle.Error
	if errors.As(err, &lifecycleError) && lifecycleError.Code != "" {
		return lifecycleError.Code
	}
	return "issuance_failed"
}

func ingressErrorCode(err error) string {
	var applyError *ingress.ApplyError
	if errors.As(err, &applyError) {
		return "Ingress" + applyError.Code
	}
	return "IngressRejected"
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
