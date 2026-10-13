package ingress

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/state"
)

func ingressStoreFixture(t *testing.T) Store {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = state.Migrate(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES('inst-0123456789abcdef','app','1','running',1,'now','now')`); err != nil {
		t.Fatal(err)
	}
	return Store{DB: db}
}

func TestStoreRejectsHostnameCollisionAndDefaultsDisabled(t *testing.T) {
	ctx := context.Background()
	store := ingressStoreFixture(t)
	route := baseRoute()
	route.DesiredState = Disabled
	created, err := store.CreateRoute(ctx, route)
	if err != nil || created.DesiredState != Disabled || created.AppliedGeneration != 0 {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	second := route
	second.ID = "route-fedcba9876543210"
	if _, err = store.CreateRoute(ctx, second); err == nil {
		t.Fatal("duplicate hostname accepted")
	}
	if _, err = store.SetDesiredState(ctx, route.ID, Enabled); err != nil {
		t.Fatal(err)
	}
	routes, err := store.ListRoutes(ctx, route.InstallationID)
	if err != nil || len(routes) != 1 || routes[0].DesiredState != Enabled {
		t.Fatalf("routes=%#v err=%v", routes, err)
	}
}

func TestStoreCommitsGenerationAtomically(t *testing.T) {
	ctx := context.Background()
	store := ingressStoreFixture(t)
	route := baseRoute()
	if _, err := store.CreateRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	first := Candidate{Generation: 1, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := store.RecordGeneration(ctx, first, "op-first", "validated", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitGeneration(ctx, first, []string{route.ID}); err != nil {
		t.Fatal(err)
	}
	second := Candidate{Generation: 2, PreviousGeneration: 1, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if err := store.RecordGeneration(ctx, second, "op-second", "validated", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitGeneration(ctx, second, []string{route.ID}); err != nil {
		t.Fatal(err)
	}
	var active, previous string
	if err := store.DB.QueryRow(`SELECT status FROM ingress_generations WHERE generation=2`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT status FROM ingress_generations WHERE generation=1`).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetRoute(ctx, route.ID)
	if err != nil || active != "active" || previous != "rolled_back" || updated.AppliedGeneration != 2 {
		t.Fatalf("active=%s previous=%s route=%#v err=%v", active, previous, updated, err)
	}
}

func TestStoreMissingRouteIsTypedNoRows(t *testing.T) {
	store := ingressStoreFixture(t)
	_, err := store.GetRoute(context.Background(), "route-0123456789abcdef")
	if err != sql.ErrNoRows {
		t.Fatalf("err=%v", err)
	}
}
