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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if os.IsExist(err) {
		return fmt.Errorf("pre-push hook already exists at %s; merge manually instead", path)
	}
	if err != nil {
		return fmt.Errorf("create pre-push hook: %w", err)
	}
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
	return nil
}
