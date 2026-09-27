package ingress

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultIngressRoot = "/var/lib/kitpro-helper/ingress"

type ConfigurationChecker func(context.Context, string, int64) error

type NginxRuntime struct {
	Root          string
	Check         ConfigurationChecker
	Reload        func(context.Context) error
	VerifyProcess func() error
}

func NewNginxRuntime() NginxRuntime {
	return NginxRuntime{
		Root:          defaultIngressRoot,
		Check:         (ValidatorClient{}).Check,
		Reload:        (ReloadBridge{}).Request,
		VerifyProcess: func() error { return verifyManagedNginx(filepath.Join(defaultIngressRoot, "run", "nginx.pid")) },
	}
}

func (n NginxRuntime) validate() error {
	if n.Root == "" {
		n.Root = defaultIngressRoot
	}
	if !filepath.IsAbs(n.Root) || filepath.Clean(n.Root) != n.Root || n.Check == nil || n.Reload == nil || n.VerifyProcess == nil {
		return fmt.Errorf("invalid ingress runtime")
	}
	return nil
}

func (n NginxRuntime) generationPath(generation int64) (string, error) {
	if err := n.validate(); err != nil {
		return "", err
	}
	if generation < 1 {
		return "", fmt.Errorf("invalid ingress generation")
	}
	return filepath.Join(n.Root, "generations", strconv.FormatInt(generation, 10)), nil
}

func (n NginxRuntime) Prepare(_ context.Context, candidate Candidate) error {
	path, err := n.generationPath(candidate.Generation)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(path, "run"), 0750); err != nil {
		return fmt.Errorf("prepare ingress run directory: %w", err)
	}
	if err = os.MkdirAll(filepath.Join(path, "logs"), 0750); err != nil {
		return fmt.Errorf("prepare ingress log directory: %w", err)
	}
	configPath := filepath.Join(path, "nginx.conf")
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return fmt.Errorf("prepare ingress configuration: %w", err)
	}
	if _, err = file.Write(candidate.Configuration); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write ingress configuration: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close ingress configuration: %w", closeErr)
	}
	return syncDirectory(path)
}

func (n NginxRuntime) Validate(ctx context.Context, candidate Candidate) error {
	if _, err := n.generationPath(candidate.Generation); err != nil {
		return err
	}
	if err := n.Check(ctx, "generation", candidate.Generation); err != nil {
		return fmt.Errorf("nginx candidate rejected: %w", err)
	}
	return nil
}

func (n NginxRuntime) Activate(ctx context.Context, candidate Candidate) error {
	path, err := n.generationPath(candidate.Generation)
	if err != nil {
		return err
	}
	if err = replaceSymlink(n.Root, path); err != nil {
		return err
	}
	return n.reload(ctx)
}

func (n NginxRuntime) Verify(ctx context.Context, candidate Candidate) error {
	return n.VerifyGeneration(ctx, candidate.Generation)
}

func (n NginxRuntime) Rollback(ctx context.Context, candidate Candidate) error {
	if candidate.PreviousGeneration == 0 {
		if err := replaceSymlink(n.Root, filepath.Join(n.Root, "bootstrap")); err != nil {
			return err
		}
		return n.reload(ctx)
	}
	path, err := n.generationPath(candidate.PreviousGeneration)
	if err != nil {
		return err
	}
	if err = replaceSymlink(n.Root, path); err != nil {
		return err
	}
	return n.reload(ctx)
}

func (n NginxRuntime) VerifyGeneration(ctx context.Context, generation int64) error {
	path, err := n.generationPath(generation)
	if err != nil {
		return err
	}
	current, err := filepath.EvalSymlinks(filepath.Join(n.Root, "current"))
	if err != nil || current != path {
		return fmt.Errorf("active ingress generation does not match")
	}
	if err := n.Check(ctx, "generation", generation); err != nil {
		return fmt.Errorf("active nginx configuration failed verification: %w", err)
	}
	if err = n.VerifyProcess(); err != nil {
		return fmt.Errorf("managed nginx process is unavailable")
	}
	return nil
}

// ReloadAndVerifyCurrent is used after atomically replacing certificate
// material referenced by the active generation. It validates before reload and
// confirms the managed process remains alive after accepting the new material.
func (n NginxRuntime) ReloadAndVerifyCurrent(ctx context.Context) error {
	if err := n.validate(); err != nil {
		return err
	}
	if err := n.Check(ctx, "current", 0); err != nil {
		return fmt.Errorf("active nginx configuration failed verification: %w", err)
	}
	if err := n.reload(ctx); err != nil {
		return err
	}
	if err := n.VerifyProcess(); err != nil {
		return fmt.Errorf("managed nginx process is unavailable")
	}
	return nil
}

func (n NginxRuntime) Cleanup(_ context.Context, candidate Candidate) error {
	if candidate.PreviousGeneration == 0 {
		return nil
	}
	path, err := n.generationPath(candidate.PreviousGeneration)
	if err != nil {
		return err
	}
	// Keep exactly the immediately previous known-good generation for recovery.
	// Older pruning is deliberately handled by a separate bounded maintenance pass.
	_ = path
	return nil
}

func (n NginxRuntime) reload(ctx context.Context) error {
	if err := n.Reload(ctx); err != nil {
		return fmt.Errorf("reload KITPro ingress: %w", err)
	}
	return nil
}

func verifyManagedNginx(pidPath string) error {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid < 2 {
		return fmt.Errorf("invalid managed nginx PID")
	}
	command, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil || strings.TrimSpace(string(command)) != "nginx" {
		return fmt.Errorf("managed nginx process is unavailable")
	}
	return nil
}

func replaceSymlink(root, target string) error {
	if err := os.MkdirAll(root, 0750); err != nil {
		return err
	}
	temporary := filepath.Join(root, ".current-new")
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("activate ingress generation: %w", err)
	}
	return syncDirectory(root)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
