package ingress

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeRuntime struct {
	failAt     string
	active     int64
	knownGood  int64
	calls      []string
	rollbackOK bool
}

func (f *fakeRuntime) call(name string, generation int64) error {
	f.calls = append(f.calls, name)
	if f.failAt == name {
		return errors.New(name)
	}
	if name == "activate" {
		f.active = generation
	}
	if name == "rollback" {
		if f.rollbackOK {
			f.active = f.knownGood
		} else {
			return errors.New("rollback")
		}
	}
	return nil
}
func (f *fakeRuntime) Prepare(_ context.Context, c Candidate) error {
	return f.call("prepare", c.Generation)
}
func (f *fakeRuntime) Validate(_ context.Context, c Candidate) error {
	return f.call("validate", c.Generation)
}
func (f *fakeRuntime) Activate(_ context.Context, c Candidate) error {
	return f.call("activate", c.Generation)
}
func (f *fakeRuntime) Verify(_ context.Context, c Candidate) error {
	return f.call("verify", c.Generation)
}
func (f *fakeRuntime) Rollback(_ context.Context, c Candidate) error {
	return f.call("rollback", c.Generation)
}
func (f *fakeRuntime) VerifyGeneration(_ context.Context, generation int64) error {
	if f.active != generation {
		return errors.New("wrong generation")
	}
	return f.call("verify-rollback", generation)
}
func (f *fakeRuntime) Cleanup(_ context.Context, c Candidate) error {
	return f.call("cleanup", c.Generation)
}

func TestManagerAppliesValidatedCandidate(t *testing.T) {
	runtime := &fakeRuntime{active: 4, knownGood: 4, rollbackOK: true}
	candidate, err := (Manager{Runtime: runtime}).Apply(context.Background(), 5, 4, []byte("config"))
	if err != nil || runtime.active != 5 || len(candidate.SHA256) != 64 {
		t.Fatalf("apply candidate=%#v active=%d err=%v", candidate, runtime.active, err)
	}
	want := []string{"prepare", "validate", "activate", "verify", "cleanup"}
	if !reflect.DeepEqual(runtime.calls, want) {
		t.Fatalf("calls=%v want=%v", runtime.calls, want)
	}
}

func TestManagerValidationFailureNeverActivates(t *testing.T) {
	runtime := &fakeRuntime{failAt: "validate", active: 4, knownGood: 4, rollbackOK: true}
	_, err := (Manager{Runtime: runtime}).Apply(context.Background(), 5, 4, []byte("bad"))
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr.Code != "validation_failed" || runtime.active != 4 || !reflect.DeepEqual(runtime.calls, []string{"prepare", "validate"}) {
		t.Fatalf("active=%d calls=%v err=%v", runtime.active, runtime.calls, err)
	}
}

func TestManagerFailedActivationAndVerificationRestoreKnownGood(t *testing.T) {
	for _, failure := range []string{"activate", "verify"} {
		t.Run(failure, func(t *testing.T) {
			runtime := &fakeRuntime{failAt: failure, active: 4, knownGood: 4, rollbackOK: true}
			_, err := (Manager{Runtime: runtime}).Apply(context.Background(), 5, 4, []byte("candidate"))
			var applyErr *ApplyError
			if !errors.As(err, &applyErr) || !applyErr.RolledBack || applyErr.RecoveryNow || runtime.active != 4 {
				t.Fatalf("known-good generation not restored: active=%d err=%#v", runtime.active, applyErr)
			}
		})
	}
}

func TestManagerRollbackFailureRequiresRecovery(t *testing.T) {
	runtime := &fakeRuntime{failAt: "verify", active: 4, knownGood: 4, rollbackOK: false}
	_, err := (Manager{Runtime: runtime}).Apply(context.Background(), 5, 4, []byte("candidate"))
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr.Code != "rollback_failed" || !applyErr.RecoveryNow {
		t.Fatalf("rollback ambiguity not surfaced: %#v", applyErr)
	}
}
