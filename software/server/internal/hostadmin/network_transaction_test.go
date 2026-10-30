package hostadmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryNetworkBackend struct {
	current             NetworkCandidate
	applyErr, verifyErr error
	applyBeforeError    bool
	applies             int
	reads               int
	changeOnSecondRead  *NetworkCandidate
}

func (b *memoryNetworkBackend) Read(context.Context, NetworkPolicyResource) (NetworkCandidate, error) {
	b.reads++
	if b.reads == 2 && b.changeOnSecondRead != nil {
		b.current = *b.changeOnSecondRead
	}
	return b.current, nil
}
func (b *memoryNetworkBackend) Apply(_ context.Context, _ NetworkPolicyResource, c NetworkCandidate) error {
	b.applies++
	if b.applyErr != nil && !b.applyBeforeError {
		return b.applyErr
	}
	b.current = CanonicalNetworkCandidate(c)
	return b.applyErr
}
func (b *memoryNetworkBackend) Verify(_ context.Context, _ NetworkPolicyResource, c NetworkCandidate) error {
	if b.verifyErr != nil {
		return b.verifyErr
	}
	if !NetworkCandidateEqual(b.current, c) {
		return ErrNetworkConflict
	}
	return nil
}

type memoryScheduler struct {
	armed             map[string]bool
	armErr, disarmErr error
}

func (s *memoryScheduler) Arm(_ context.Context, id string, _ time.Time) error {
	if s.armErr != nil {
		return s.armErr
	}
	s.armed[id] = true
	return nil
}
func (s *memoryScheduler) Disarm(_ context.Context, id string) error {
	if s.disarmErr != nil {
		return s.disarmErr
	}
	delete(s.armed, id)
	return nil
}

func transactionFixture(t *testing.T) (*NetworkTransactionController, *memoryNetworkBackend, *memoryScheduler, NetworkPolicyResource) {
	t.Helper()
	resource := approvedNetworkResource()
	backend := &memoryNetworkBackend{current: NetworkCandidate{Mode: NetworkModeDHCP}}
	scheduler := &memoryScheduler{armed: map[string]bool{}}
	controller := NewNetworkTransactionController(NetworkPolicy{Version: 1, Resources: []NetworkPolicyResource{resource}}, backend, scheduler)
	controller.StateDir = t.TempDir()
	if err := os.Chmod(controller.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	controller.allowTestOwnership = true
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	controller.Now = func() time.Time { return now }
	return controller, backend, scheduler, resource
}

func TestNetworkTransactionApplyConfirmAndExactReceipt(t *testing.T) {
	controller, backend, scheduler, resource := transactionFixture(t)
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-0001", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if tx.State != "awaiting_confirmation" || !tx.RecoveryArmed || !scheduler.armed[tx.ID] {
		t.Fatalf("tx=%#v scheduler=%#v", tx, scheduler)
	}
	if _, err = controller.Confirm(context.Background(), tx.ID, "wrong"); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("wrong receipt err=%v", err)
	}
	tx, err = controller.Confirm(context.Background(), tx.ID, tx.Candidate.Digest)
	if err != nil || tx.State != "confirmed" || tx.RecoveryArmed || scheduler.armed[tx.ID] {
		t.Fatalf("confirmed=%#v err=%v", tx, err)
	}
}

func TestNetworkTransactionCreatesProtectedRecoveryDirectory(t *testing.T) {
	controller, backend, _, resource := transactionFixture(t)
	controller.StateDir = filepath.Join(t.TempDir(), "network-transactions")
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-directory", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(controller.StateDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("recovery directory info=%#v err=%v", info, err)
	}
	if _, err = os.Stat(controller.recordPath(tx.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkTransactionTimeoutRollsBackAndRefusesExternalChange(t *testing.T) {
	controller, backend, _, resource := transactionFixture(t)
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-0002", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	tx, err = controller.Rollback(context.Background(), tx.ID, "confirmation deadline expired")
	if err != nil || tx.State != "rolled_back" || backend.current.Mode != NetworkModeDHCP {
		t.Fatalf("rollback=%#v current=%#v err=%v", tx, backend.current, err)
	}
	controller, backend, _, resource = transactionFixture(t)
	tx, err = controller.Apply(context.Background(), "network-operation-0003", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	backend.current = NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"198.51.100.9/24"}}
	tx, err = controller.Rollback(context.Background(), tx.ID, "deadline")
	if !errors.Is(err, ErrNetworkConflict) || tx.State != "outcome_unknown" {
		t.Fatalf("external rollback=%#v err=%v", tx, err)
	}
}

func TestNetworkTransactionFailsBeforeApplyWhenRecoveryCannotArm(t *testing.T) {
	controller, backend, scheduler, resource := transactionFixture(t)
	scheduler.armErr = errors.New("timer unavailable")
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-0004", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err == nil || tx.State != "failed" || backend.applies != 0 {
		t.Fatalf("tx=%#v applies=%d err=%v", tx, backend.applies, err)
	}
}

func TestNetworkTransactionReconcilesVerifiedRestoreAfterBackendError(t *testing.T) {
	controller, backend, scheduler, resource := transactionFixture(t)
	backend.applyErr = errors.New("backend reload failed")
	backend.applyBeforeError = true
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-reconcile", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil || tx.State != "rolled_back" || tx.Rollback != "completed" || tx.RecoveryArmed || scheduler.armed[tx.ID] || !NetworkCandidateEqual(backend.current, NetworkCandidate{Mode: NetworkModeDHCP}) {
		t.Fatalf("tx=%#v current=%#v armed=%v err=%v", tx, backend.current, scheduler.armed, err)
	}
}

func TestNetworkTransactionRefusesChangeAfterRecoveryArm(t *testing.T) {
	controller, backend, scheduler, resource := transactionFixture(t)
	external := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"198.51.100.8/24"}}
	backend.changeOnSecondRead = &external
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-race01", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if !errors.Is(err, ErrNetworkConflict) || tx.State != "outcome_unknown" || tx.RecoveryArmed || backend.applies != 0 || scheduler.armed[tx.ID] {
		t.Fatalf("tx=%#v applies=%d armed=%v err=%v", tx, backend.applies, scheduler.armed, err)
	}
}

func TestNetworkTransactionStartupRearmsThenExpires(t *testing.T) {
	controller, backend, _, resource := transactionFixture(t)
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	tx, err := controller.Apply(context.Background(), "network-operation-restart1", resource.ID, NetworkConfigurationDigest(resource, backend.current), candidate, controller.Now().Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	replacement := &memoryScheduler{armed: map[string]bool{}}
	controller.Scheduler = replacement
	count, err := controller.RecoverPending(context.Background())
	if err != nil || count != 1 || !replacement.armed[tx.ID] {
		t.Fatalf("rearm count=%d armed=%v err=%v", count, replacement.armed, err)
	}
	base := controller.Now()
	controller.Now = func() time.Time { return base.Add(2 * time.Minute) }
	count, err = controller.RecoverPending(context.Background())
	if err != nil || count != 1 || backend.current.Mode != NetworkModeDHCP {
		t.Fatalf("expiry count=%d current=%#v err=%v", count, backend.current, err)
	}
}

func TestNetworkdFileBackendRejectsUnknownAndPublishesAtomically(t *testing.T) {
	dir := t.TempDir()
	resource := approvedNetworkResource()
	initial := NetworkCandidate{Mode: NetworkModeDHCP}
	raw, _ := NetworkdProfile(resource, initial)
	if err := os.WriteFile(filepath.Join(dir, resource.Profile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	reloads, reconfigures := 0, 0
	backend := NetworkdFileBackend{Directory: dir, allowTestOwnership: true, Reload: func(context.Context) error { reloads++; return nil }, Reconfigure: func(_ context.Context, name string) error {
		reconfigures++
		if name != resource.Interface {
			t.Fatal(name)
		}
		return nil
	}}
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	if err := backend.Apply(context.Background(), resource, candidate); err != nil {
		t.Fatal(err)
	}
	if err := backend.Verify(context.Background(), resource, candidate); err != nil || reloads != 1 || reconfigures != 1 {
		t.Fatalf("verify=%v reload=%d reconfigure=%d", err, reloads, reconfigures)
	}
	if err := os.WriteFile(filepath.Join(dir, resource.Profile), []byte("[Match]\nName=other0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Read(context.Background(), resource); err == nil {
		t.Fatal("foreign profile accepted")
	}
}

func TestNetworkdFileBackendRequiresStableExactAddressConvergence(t *testing.T) {
	dir := t.TempDir()
	resource := approvedNetworkResource()
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}}
	raw, _ := NetworkdProfile(resource, candidate)
	if err := os.WriteFile(filepath.Join(dir, resource.Profile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	observations := [][]string{
		{"192.0.2.20/24"},
		{"192.0.2.40/24"},
		{"192.0.2.20/24", "192.0.2.40/24"},
		{"192.0.2.20/24"},
		{"192.0.2.20/24"},
	}
	index := 0
	backend := NetworkdFileBackend{Directory: dir, allowTestOwnership: true, ConvergenceChecks: len(observations), ConvergenceWait: func(context.Context) error { return nil }, ObserveAddresses: func(string) ([]string, error) {
		value := observations[index]
		index++
		return value, nil
	}}
	if err := backend.Verify(context.Background(), resource, candidate); err != nil || index != len(observations) {
		t.Fatalf("verify=%v observations=%d", err, index)
	}
	backend.ConvergenceChecks = 2
	backend.ObserveAddresses = func(string) ([]string, error) { return []string{"192.0.2.40/24"}, nil }
	if err := backend.Verify(context.Background(), resource, candidate); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("stale address accepted: %v", err)
	}
}

func TestSystemdNetworkRecoverySchedulerPublishesIndependentTimer(t *testing.T) {
	dir := t.TempDir()
	scheduler := NewSystemdNetworkRecoveryScheduler()
	scheduler.UnitDir = dir
	scheduler.HelperPath = "/usr/libexec/kitpro-helper"
	scheduler.allowTestOwnership = true
	reloaded, started, stopped := 0, "", ""
	scheduler.Reload = func(context.Context) error { reloaded++; return nil }
	scheduler.Start = func(_ context.Context, name string) error { started = name; return nil }
	scheduler.Stop = func(_ context.Context, name string) error { stopped = name; return nil }
	id := "network-operation-rollback1"
	if err := scheduler.Arm(context.Background(), id, time.Now().Add(45*time.Second)); err != nil {
		t.Fatal(err)
	}
	service, err := os.ReadFile(filepath.Join(dir, networkRecoveryUnit(id, "service")))
	if err != nil {
		t.Fatal(err)
	}
	timer, err := os.ReadFile(filepath.Join(dir, networkRecoveryUnit(id, "timer")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), "AppArmorProfile=/usr/libexec/kitpro-helper") || !strings.Contains(string(service), "--network-recover "+id) || !strings.Contains(string(timer), "OnActiveSec=") || started != networkRecoveryUnit(id, "timer") || reloaded != 1 {
		t.Fatalf("service=%s timer=%s started=%s reload=%d", service, timer, started, reloaded)
	}
	if err = scheduler.Disarm(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if stopped != networkRecoveryUnit(id, "timer") || reloaded != 2 {
		t.Fatalf("stopped=%s reload=%d", stopped, reloaded)
	}
}
