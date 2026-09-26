package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type acceptingIngressRuntime struct{}

func (acceptingIngressRuntime) Prepare(context.Context, ingress.Candidate) error  { return nil }
func (acceptingIngressRuntime) Validate(context.Context, ingress.Candidate) error { return nil }
func (acceptingIngressRuntime) Activate(context.Context, ingress.Candidate) error { return nil }
func (acceptingIngressRuntime) Verify(context.Context, ingress.Candidate) error   { return nil }
func (acceptingIngressRuntime) Rollback(context.Context, ingress.Candidate) error { return nil }
func (acceptingIngressRuntime) VerifyGeneration(context.Context, int64) error     { return nil }
func (acceptingIngressRuntime) Cleanup(context.Context, ingress.Candidate) error  { return nil }

func TestApplyIngressGenerationUsesMigratedHelperSchema(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,created_at,cleanup_state) VALUES('inst-actual01',2,'op-runtime','actual-budget','26.9.0','active','kitpro-net','/srv/kitpro/apps/actual-budget/inst-actual01/data','now','clean')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runtime_generation_bindings(installation_id,runtime_generation,service_id,transport,container_port,mode,host_address,host_port) VALUES('inst-actual01',2,'web','tcp',5006,'loopback','127.0.0.1',20000)`); err != nil {
		t.Fatal(err)
	}
	originalRuntime := ingressRuntime
	defer func() { ingressRuntime = originalRuntime }()
	ingressRuntime = func() ingress.Runtime { return acceptingIngressRuntime{} }

	result, err := applyIngressGeneration(context.Background(), db, protocol.Request{
		OperationID: "op-ingress01",
		IngressRoutes: []protocol.IngressRoute{{
			RouteID: "route-0123456789abcdef", InstallationID: "inst-actual01", ServiceID: "web",
			Hostname: "actual.example.test", ExposureMode: "lan", UpstreamProtocol: "http", TLSPolicy: "none",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["generation"] != int64(1) {
		t.Fatalf("unexpected result: %#v", result)
	}
	var status, generationPath, validatedAt, activatedAt string
	if err = db.QueryRow(`SELECT status,generation_path,validated_at,activated_at FROM ingress_generations WHERE generation=1`).Scan(&status, &generationPath, &validatedAt, &activatedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || generationPath != "/var/lib/kitpro-helper/ingress/generations/1" || validatedAt == "" || activatedAt == "" {
		t.Fatalf("unexpected generation state: status=%q path=%q validated=%q activated=%q", status, generationPath, validatedAt, activatedAt)
	}
	var backendAddress string
	var backendPort int
	if err = db.QueryRow(`SELECT backend_address,backend_port FROM ingress_generation_routes WHERE generation=1 AND route_id='route-0123456789abcdef'`).Scan(&backendAddress, &backendPort); err != nil {
		t.Fatal(err)
	}
	if backendAddress != "127.0.0.1" || backendPort != 20000 {
		t.Fatalf("unsafe backend projection: %s:%d", backendAddress, backendPort)
	}
}
