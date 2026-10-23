// Package repocontext gathers working-tree context for code review.
package repocontext

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Diff returns the unified diff of staged and unstaged changes against base.
// It runs in the current working directory; untracked files are not included.
func Diff(base string) (string, error) { return diffAt("", base, false) }

// TouchedFiles returns repo-relative paths changed against base, in Git order.
// It runs in the current working directory; untracked files are not included.
func TouchedFiles(base string) ([]string, error) { return touchedFilesAt("", base) }

func touchedFilesAt(root, base string) ([]string, error) {
	out, err := diffAt(root, base, true)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

func diffAt(root, base string, names bool) (string, error) {
	if base == "" || strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("invalid diff base %q: expected a Git ref", base)
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if names {
		args = append(args, "--name-only", "-z")
	} else {
		args = append(args, "--no-color")
	}
	args = append(args, "--end-of-options", base, "--")
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff against %q: %w: %s", base, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
