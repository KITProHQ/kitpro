package ingress

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const defaultIngressRoot = "/var/lib/kitpro-helper/ingress"

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type NginxRuntime struct {
	Root string
	Run  CommandRunner
}

func NewNginxRuntime() NginxRuntime {
	return NginxRuntime{Root: defaultIngressRoot, Run: runCommand}
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (n NginxRuntime) validate() error {
	if n.Root == "" {
		n.Root = defaultIngressRoot
	}
	if !filepath.IsAbs(n.Root) || filepath.Clean(n.Root) != n.Root || n.Run == nil {
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
	path, err := n.generationPath(candidate.Generation)
	if err != nil {
		return err
	}
	output, err := n.Run(ctx, "/usr/sbin/nginx", "-t", "-p", path+"/", "-c", "nginx.conf")
	if err != nil {
		return fmt.Errorf("nginx candidate rejected: %s", boundedOutput(output))
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
		if err := os.Remove(filepath.Join(n.Root, "current")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove failed ingress activation: %w", err)
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
	output, err := n.Run(ctx, "/usr/sbin/nginx", "-t", "-p", n.Root+"/", "-c", "current/nginx.conf")
	if err != nil {
		return fmt.Errorf("active nginx configuration failed verification: %s", boundedOutput(output))
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
	output, err := n.Run(ctx, "/usr/bin/systemctl", "reload-or-restart", "kitpro-ingress.service")
	if err != nil {
		return fmt.Errorf("reload KITPro ingress: %s", boundedOutput(output))
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

func boundedOutput(value []byte) string {
	const limit = 2048
	if len(value) > limit {
		value = value[:limit]
	}
	return string(value)
}
