package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type Candidate struct {
	Generation         int64
	PreviousGeneration int64
	Configuration      []byte
	SHA256             string
}

type Runtime interface {
	Prepare(context.Context, Candidate) error
	Validate(context.Context, Candidate) error
	Activate(context.Context, Candidate) error
	Verify(context.Context, Candidate) error
	Rollback(context.Context, Candidate) error
	VerifyGeneration(context.Context, int64) error
	Cleanup(context.Context, Candidate) error
}

type ApplyError struct {
	Code        string
	RolledBack  bool
	RecoveryNow bool
	Cause       error
}

func (e *ApplyError) Error() string { return fmt.Sprintf("%s: %v", e.Code, e.Cause) }
func (e *ApplyError) Unwrap() error { return e.Cause }

type Manager struct{ Runtime Runtime }

func (m Manager) Apply(ctx context.Context, generation, previous int64, configuration []byte) (Candidate, error) {
	if m.Runtime == nil || generation < 1 || previous < 0 || generation == previous || len(configuration) == 0 || len(configuration) > 1024*1024 {
		return Candidate{}, &ApplyError{Code: "invalid_candidate", Cause: fmt.Errorf("invalid ingress candidate")}
	}
	digest := sha256.Sum256(configuration)
	candidate := Candidate{Generation: generation, PreviousGeneration: previous, Configuration: append([]byte(nil), configuration...), SHA256: hex.EncodeToString(digest[:])}
	if err := m.Runtime.Prepare(ctx, candidate); err != nil {
		return candidate, &ApplyError{Code: "prepare_failed", Cause: err}
	}
	if err := m.Runtime.Validate(ctx, candidate); err != nil {
		return candidate, &ApplyError{Code: "validation_failed", Cause: err}
	}
	if err := m.Runtime.Activate(ctx, candidate); err != nil {
		return candidate, m.rollback(ctx, candidate, "activation_failed", err)
	}
	if err := m.Runtime.Verify(ctx, candidate); err != nil {
		return candidate, m.rollback(ctx, candidate, "verification_failed", err)
	}
	if err := m.Runtime.Cleanup(ctx, candidate); err != nil {
		return candidate, &ApplyError{Code: "cleanup_pending", Cause: err}
	}
	return candidate, nil
}

func (m Manager) rollback(ctx context.Context, candidate Candidate, code string, cause error) error {
	if err := m.Runtime.Rollback(ctx, candidate); err != nil {
		return &ApplyError{Code: "rollback_failed", RecoveryNow: true, Cause: fmt.Errorf("%s: %w; rollback: %v", code, cause, err)}
	}
	if candidate.PreviousGeneration > 0 {
		if err := m.Runtime.VerifyGeneration(ctx, candidate.PreviousGeneration); err != nil {
			return &ApplyError{Code: "rollback_unverified", RecoveryNow: true, Cause: fmt.Errorf("%s: %w; rollback verification: %v", code, cause, err)}
		}
	}
	return &ApplyError{Code: code, RolledBack: true, Cause: cause}
}
