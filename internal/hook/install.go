// Package hook installs an opt-in Git pre-push review hook.
package hook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install relies on esuna being on PATH. Existing hooks (including symlinks)
// are refused, never overwritten: merge manually. Git resolves the effective
// hooks directory, including worktrees and core.hooksPath. Advisory hooks do
// not block pushes, even on tool errors; --gate hooks propagate esuna's status.
func Install(targetRepoRoot string, gate bool) error {
	if out, err := exec.Command("git", "-C", targetRepoRoot, "rev-parse", "--show-toplevel").CombinedOutput(); err != nil {
		return fmt.Errorf("target must be a working Git repository: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("git", "-C", targetRepoRoot, "rev-parse", "--path-format=absolute", "--git-path", "hooks").Output()
	if err != nil {
		return fmt.Errorf("resolve Git hooks directory: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	path := filepath.Join(dir, "pre-push")
	// Stage privately and non-executable: failed writes must never publish a hook.
	file, err := os.CreateTemp(dir, ".pre-push-*")
	if err != nil {
		return fmt.Errorf("create temporary pre-push hook: %w", err)
	}
	defer os.Remove(file.Name())
	script := "#!/bin/sh\n# Requires esuna on PATH. Review from the repository root.\ncd \"$(git rev-parse --show-toplevel)\" || exit 1\n"
	if gate {
		script += "exec esuna --gate </dev/null\n"
	} else {
		script += "esuna </dev/null\n# Advisory: findings and tool errors do not block pushes.\nexit 0\n"
	}
	_, writeErr := file.WriteString(script)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write pre-push hook: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close pre-push hook: %w", closeErr)
	}
	if err := os.Chmod(file.Name(), 0755); err != nil {
		return fmt.Errorf("make pre-push hook executable: %w", err)
	}
	// Link atomically publishes the completed script without replacing an
	// existing hook (unlike Rename), including one installed concurrently.
	if err := os.Link(file.Name(), path); os.IsExist(err) {
		return fmt.Errorf("pre-push hook already exists at %s; merge manually instead", path)
	} else if err != nil {
		return fmt.Errorf("publish pre-push hook: %w", err)
	}
	return nil
}
