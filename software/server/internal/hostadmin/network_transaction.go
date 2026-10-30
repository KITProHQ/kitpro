package hostadmin

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type NetworkBackend interface {
	Read(context.Context, NetworkPolicyResource) (NetworkCandidate, error)
	Apply(context.Context, NetworkPolicyResource, NetworkCandidate) error
	Verify(context.Context, NetworkPolicyResource, NetworkCandidate) error
}

type NetworkRecoveryScheduler interface {
	Arm(context.Context, string, time.Time) error
	Disarm(context.Context, string) error
}

type NetworkTransactionController struct {
	Policy             NetworkPolicy
	Backend            NetworkBackend
	Scheduler          NetworkRecoveryScheduler
	StateDir           string
	Now                func() time.Time
	SafetyCheck        func(context.Context, NetworkPolicyResource) error
	allowTestOwnership bool
	mu                 sync.Mutex
}

type networkRecoveryRecord struct {
	Version     int                `json:"version"`
	Transaction NetworkTransaction `json:"transaction"`
}

func NewNetworkTransactionController(policy NetworkPolicy, backend NetworkBackend, scheduler NetworkRecoveryScheduler) *NetworkTransactionController {
	return &NetworkTransactionController{Policy: policy, Backend: backend, Scheduler: scheduler, StateDir: "/var/lib/kitpro-helper/network-transactions", Now: time.Now}
}

func (c *NetworkTransactionController) recordPath(id string) string {
	return filepath.Join(c.StateDir, id+".json")
}

func (c *NetworkTransactionController) Apply(ctx context.Context, id, resourceID, expectedDigest string, candidate NetworkCandidate, deadline time.Time) (NetworkTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ValidateNetworkTransactionID(id) != nil || c.Backend == nil || c.Scheduler == nil {
		return NetworkTransaction{}, ErrInvalid
	}
	resource, ok := c.Policy.Resource(resourceID)
	if !ok || resource.ManagementProtected {
		return NetworkTransaction{}, ErrPermission
	}
	if c.SafetyCheck != nil {
		if err := c.SafetyCheck(ctx, resource); err != nil {
			return NetworkTransaction{}, err
		}
	}
	if err := ValidateNetworkCandidate(resource, candidate); err != nil {
		return NetworkTransaction{}, err
	}
	now := c.Now().UTC()
	if deadline.Before(now.Add(15*time.Second)) || deadline.After(now.Add(5*time.Minute)) {
		return NetworkTransaction{}, ErrInvalid
	}
	previous, err := c.Backend.Read(ctx, resource)
	if err != nil {
		return NetworkTransaction{}, err
	}
	previousConfig := NetworkConfiguration{ResourceID: resource.ID, Interface: resource.Interface, Backend: resource.Backend, Profile: resource.Profile, Candidate: CanonicalNetworkCandidate(previous), Digest: NetworkConfigurationDigest(resource, previous)}
	if previousConfig.Digest != expectedDigest {
		return NetworkTransaction{}, ErrNetworkConflict
	}
	proposed := NetworkConfiguration{ResourceID: resource.ID, Interface: resource.Interface, Backend: resource.Backend, Profile: resource.Profile, Candidate: CanonicalNetworkCandidate(candidate), Digest: NetworkConfigurationDigest(resource, candidate)}
	tx := NetworkTransaction{ID: id, ResourceID: resource.ID, Interface: resource.Interface, Backend: resource.Backend, Previous: previousConfig, Candidate: proposed, State: "recovery_arming", Deadline: deadline.Format(time.RFC3339Nano), Connectivity: "verification_pending", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}
	if err = ensurePersistenceDirectory(c.StateDir, 0700, true, c.allowTestOwnership); err != nil {
		return NetworkTransaction{}, err
	}
	if err = c.save(tx, false); err != nil {
		return NetworkTransaction{}, err
	}
	if err = c.Scheduler.Arm(ctx, id, deadline); err != nil {
		tx.State = "failed"
		tx.FailureReason = "independent rollback could not be armed"
		_ = c.save(tx, true)
		return tx, err
	}
	tx.RecoveryArmed = true
	tx.State = "applying"
	tx.UpdatedAt = c.Now().UTC().Format(time.RFC3339Nano)
	if err = c.save(tx, true); err != nil {
		_ = c.Scheduler.Disarm(ctx, id)
		return tx, err
	}
	latest, latestErr := c.Backend.Read(ctx, resource)
	if latestErr != nil || NetworkConfigurationDigest(resource, latest) != previousConfig.Digest {
		_ = c.Scheduler.Disarm(ctx, id)
		tx.RecoveryArmed = false
		tx.State = "outcome_unknown"
		tx.FailureReason = "network configuration changed after recovery was armed; publication was refused"
		_ = c.save(tx, true)
		return tx, ErrNetworkConflict
	}
	if err = c.Backend.Apply(ctx, resource, candidate); err != nil {
		return c.rollbackLocked(ctx, tx, "candidate application failed")
	}
	if err = c.Backend.Verify(ctx, resource, candidate); err != nil {
		return c.rollbackLocked(ctx, tx, "candidate verification failed")
	}
	tx.State = "awaiting_confirmation"
	tx.Connectivity = "configuration_verified"
	tx.UpdatedAt = c.Now().UTC().Format(time.RFC3339Nano)
	if err = c.save(tx, true); err != nil {
		return c.rollbackLocked(ctx, tx, "transaction state could not be persisted")
	}
	return tx, nil
}

func (c *NetworkTransactionController) Confirm(ctx context.Context, id, candidateDigest string) (NetworkTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.load(id)
	if err != nil {
		return tx, err
	}
	if tx.State != "awaiting_confirmation" || tx.Candidate.Digest != candidateDigest {
		return tx, ErrNetworkConflict
	}
	deadline, err := time.Parse(time.RFC3339Nano, tx.Deadline)
	if err != nil || !c.Now().Before(deadline) {
		return c.rollbackLocked(ctx, tx, "confirmation deadline expired")
	}
	resource, ok := c.Policy.Resource(tx.ResourceID)
	if !ok {
		return c.rollbackLocked(ctx, tx, "trusted network policy changed")
	}
	if err = c.Backend.Verify(ctx, resource, tx.Candidate.Candidate); err != nil {
		return c.rollbackLocked(ctx, tx, "candidate state changed before confirmation")
	}
	if err = c.Scheduler.Disarm(ctx, id); err != nil {
		tx.State = "outcome_unknown"
		tx.FailureReason = "rollback timer could not be disarmed"
		_ = c.save(tx, true)
		return tx, err
	}
	tx.RecoveryArmed = false
	tx.State = "confirmed"
	tx.Connectivity = "configuration_verified"
	tx.UpdatedAt = c.Now().UTC().Format(time.RFC3339Nano)
	err = c.save(tx, true)
	return tx, err
}

func (c *NetworkTransactionController) Rollback(ctx context.Context, id, reason string) (NetworkTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.load(id)
	if err != nil {
		return tx, err
	}
	return c.rollbackLocked(ctx, tx, reason)
}

func (c *NetworkTransactionController) rollbackLocked(ctx context.Context, tx NetworkTransaction, reason string) (NetworkTransaction, error) {
	if tx.State == "rolled_back" {
		return tx, nil
	}
	resource, ok := c.Policy.Resource(tx.ResourceID)
	if !ok {
		tx.State = "outcome_unknown"
		tx.FailureReason = "trusted network policy no longer contains the resource"
		_ = c.save(tx, true)
		return tx, ErrPermission
	}
	current, err := c.Backend.Read(ctx, resource)
	if err != nil {
		tx.State = "outcome_unknown"
		tx.FailureReason = "current network state could not be read before rollback"
		_ = c.save(tx, true)
		return tx, err
	}
	currentDigest := NetworkConfigurationDigest(resource, current)
	if currentDigest != tx.Candidate.Digest && currentDigest != tx.Previous.Digest {
		_ = c.Scheduler.Disarm(ctx, tx.ID)
		tx.RecoveryArmed = false
		tx.State = "outcome_unknown"
		tx.FailureReason = "network configuration changed externally; automatic restoration was refused"
		_ = c.save(tx, true)
		return tx, ErrNetworkConflict
	}
	if currentDigest != tx.Previous.Digest {
		if err = c.Backend.Apply(ctx, resource, tx.Previous.Candidate); err != nil {
			// A backend can publish the previous configuration and then report a
			// reload error. Reconcile saved and live state before declaring failure.
			if verifyErr := c.Backend.Verify(ctx, resource, tx.Previous.Candidate); verifyErr == nil {
				_ = c.Scheduler.Disarm(ctx, tx.ID)
				tx.RecoveryArmed = false
				tx.State = "rolled_back"
				tx.Rollback = "completed"
				tx.Connectivity = "previous_configuration_verified"
				tx.FailureReason = reason + ": backend reported an error after the previous configuration was restored"
				tx.UpdatedAt = c.Now().UTC().Format(time.RFC3339Nano)
				return tx, c.save(tx, true)
			}
			tx.State = "rollback_failed"
			tx.Rollback = "failed"
			tx.FailureReason = reason + ": previous configuration could not be restored"
			_ = c.save(tx, true)
			return tx, err
		}
	}
	if err = c.Backend.Verify(ctx, resource, tx.Previous.Candidate); err != nil {
		tx.State = "rollback_failed"
		tx.Rollback = "failed"
		tx.FailureReason = reason + ": restored configuration could not be verified"
		_ = c.save(tx, true)
		return tx, err
	}
	_ = c.Scheduler.Disarm(ctx, tx.ID)
	tx.RecoveryArmed = false
	tx.State = "rolled_back"
	tx.Rollback = "completed"
	tx.Connectivity = "previous_configuration_verified"
	tx.FailureReason = reason
	tx.UpdatedAt = c.Now().UTC().Format(time.RFC3339Nano)
	err = c.save(tx, true)
	return tx, err
}

func (c *NetworkTransactionController) Get(id string) (NetworkTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load(id)
}

func (c *NetworkTransactionController) RecoverPending(ctx context.Context) (int, error) {
	c.mu.Lock()
	entries, err := os.ReadDir(c.StateDir)
	c.mu.Unlock()
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		tx, loadErr := c.Get(id)
		if loadErr != nil {
			return recovered, loadErr
		}
		if tx.State != "awaiting_confirmation" && tx.State != "applying" && tx.State != "recovery_arming" {
			continue
		}
		deadline, deadlineErr := time.Parse(time.RFC3339Nano, tx.Deadline)
		if deadlineErr != nil || !c.Now().Before(deadline) || tx.State != "awaiting_confirmation" {
			if _, rollbackErr := c.Rollback(ctx, id, "interrupted or expired network transaction recovered at helper startup"); rollbackErr != nil {
				return recovered, rollbackErr
			}
			recovered++
			continue
		}
		_ = c.Scheduler.Disarm(ctx, id)
		if armErr := c.Scheduler.Arm(ctx, id, deadline); armErr != nil {
			if _, rollbackErr := c.Rollback(ctx, id, "network rollback deadline could not be rearmed after helper restart"); rollbackErr != nil {
				return recovered, rollbackErr
			}
			recovered++
			continue
		}
		recovered++
	}
	return recovered, nil
}

func (c *NetworkTransactionController) save(tx NetworkTransaction, replace bool) error {
	if ValidateNetworkTransactionID(tx.ID) != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(networkRecoveryRecord{Version: 1, Transaction: tx})
	if err != nil || len(raw) > 64*1024 {
		return ErrInvalid
	}
	return atomicRootFile(c.recordPath(tx.ID), append(raw, '\n'), 0600, replace, c.allowTestOwnership)
}

func (c *NetworkTransactionController) load(id string) (NetworkTransaction, error) {
	if ValidateNetworkTransactionID(id) != nil {
		return NetworkTransaction{}, ErrInvalid
	}
	raw, err := readBoundedRegular(c.recordPath(id), 64*1024)
	if err != nil {
		return NetworkTransaction{}, err
	}
	var record networkRecoveryRecord
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&record) != nil || dec.Decode(&struct{}{}) != io.EOF || record.Version != 1 || record.Transaction.ID != id {
		return NetworkTransaction{}, ErrInvalid
	}
	return record.Transaction, nil
}

type NetworkdFileBackend struct {
	Directory          string
	Reload             func(context.Context) error
	Reconfigure        func(context.Context, string) error
	ObserveAddresses   func(string) ([]string, error)
	ConvergenceChecks  int
	ConvergenceWait    func(context.Context) error
	allowTestOwnership bool
}

func (b NetworkdFileBackend) path(resource NetworkPolicyResource) string {
	return filepath.Join(b.Directory, resource.Profile)
}
func (b NetworkdFileBackend) Read(_ context.Context, resource NetworkPolicyResource) (NetworkCandidate, error) {
	raw, err := readBoundedRegular(b.path(resource), 32*1024)
	if err != nil {
		return NetworkCandidate{}, err
	}
	return ParseNetworkdProfile(resource, raw)
}
func (b NetworkdFileBackend) Apply(ctx context.Context, resource NetworkPolicyResource, candidate NetworkCandidate) error {
	raw, err := NetworkdProfile(resource, candidate)
	if err != nil {
		return err
	}
	if err = atomicRootFile(b.path(resource), raw, 0644, true, b.allowTestOwnership); err != nil {
		return err
	}
	if b.Reload == nil || b.Reconfigure == nil {
		return ErrUnsupported
	}
	if err = b.Reload(ctx); err != nil {
		return err
	}
	return b.Reconfigure(ctx, resource.Interface)
}
func (b NetworkdFileBackend) Verify(ctx context.Context, resource NetworkPolicyResource, candidate NetworkCandidate) error {
	observed, err := b.Read(ctx, resource)
	if err != nil {
		return err
	}
	if !NetworkCandidateEqual(observed, candidate) {
		return ErrNetworkConflict
	}
	if candidate.Mode == NetworkModeStatic && b.ObserveAddresses != nil {
		checks := b.ConvergenceChecks
		if checks <= 0 {
			checks = 30
		}
		stable := 0
		for attempt := 0; attempt < checks; attempt++ {
			addresses, observeErr := b.ObserveAddresses(resource.Interface)
			if observeErr != nil {
				return observeErr
			}
			if exactIPv4Prefixes(addresses, candidate.Addresses) {
				stable++
			} else {
				stable = 0
			}
			if stable >= 2 {
				return nil
			}
			if attempt+1 < checks {
				if b.ConvergenceWait != nil {
					if waitErr := b.ConvergenceWait(ctx); waitErr != nil {
						return waitErr
					}
				} else {
					timer := time.NewTimer(100 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
				}
			}
		}
		return ErrNetworkConflict
	}
	return nil
}

func exactIPv4Prefixes(observed, expected []string) bool {
	want := map[string]bool{}
	for _, raw := range expected {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() {
			return false
		}
		want[prefix.String()] = true
	}
	seen := map[string]bool{}
	for _, raw := range observed {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		seen[prefix.String()] = true
	}
	if len(seen) != len(want) {
		return false
	}
	for value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}

func ParseNetworkdProfile(resource NetworkPolicyResource, raw []byte) (NetworkCandidate, error) {
	if len(raw) > 32*1024 {
		return NetworkCandidate{}, ErrInvalid
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	section := ""
	candidate := NetworkCandidate{}
	matched := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return NetworkCandidate{}, ErrInvalid
		}
		key, value := parts[0], parts[1]
		switch section + "/" + key {
		case "[Match]/Name":
			if value != resource.Interface {
				return NetworkCandidate{}, ErrPermission
			}
			matched = true
		case "[Network]/DHCP":
			if value == "ipv4" {
				candidate.Mode = NetworkModeDHCP
			} else if value == "no" {
				candidate.Mode = NetworkModeStatic
			} else {
				return NetworkCandidate{}, ErrInvalid
			}
		case "[Network]/Address":
			candidate.Addresses = append(candidate.Addresses, value)
		case "[Network]/Gateway":
			candidate.Gateway = value
		case "[Network]/DNS":
			candidate.DNSServers = append(candidate.DNSServers, value)
		case "[Network]/Domains":
			candidate.SearchDomains = append(candidate.SearchDomains, value)
		case "[Network]/IPv6AcceptRA":
			if value != "no" {
				return NetworkCandidate{}, ErrInvalid
			}
		case "[Network]/LinkLocalAddressing":
			if value != "no" {
				return NetworkCandidate{}, ErrInvalid
			}
		default:
			return NetworkCandidate{}, ErrInvalid
		}
	}
	if scanner.Err() != nil || !matched || ValidateNetworkCandidate(resource, candidate) != nil {
		return NetworkCandidate{}, ErrInvalid
	}
	return CanonicalNetworkCandidate(candidate), nil
}
