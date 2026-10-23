// Package reviewer invokes logged-in reviewer CLIs and enforces author exclusion.
// Runners inherit the working directory and existing CLI login configuration.
package reviewer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Model identifies a reviewer CLI, not a version-specific model name.
type Model string

const (
	Claude Model = "claude"
	Codex  Model = "codex"
	Muse   Model = "muse"
	Grok   Model = "grok"
)

var AllModels = []Model{Claude, Codex, Muse, Grok}

// Runner returns raw stdout; interpreting findings belongs to the pipeline.
type Runner interface {
	Model() Model
	Review(ctx context.Context, prompt string) (rawOutput string, err error)
}

func runCommand(ctx context.Context, model Model, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, string(model), args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return stdout.String(), fmt.Errorf("%s reviewer: %w (stderr: %s)", model, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runPromptFile keeps large prompts out of argv and removes the private file
// on success, subprocess failure, or cancellation.
func runPromptFile(ctx context.Context, model Model, prompt string, args ...string) (string, error) {
	file, err := os.CreateTemp("", "esuna-review-prompt-*")
	if err != nil {
		return "", fmt.Errorf("%s reviewer prompt: %w", model, err)
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(prompt); err != nil {
		file.Close()
		return "", fmt.Errorf("%s reviewer prompt: %w", model, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("%s reviewer prompt: %w", model, err)
	}
	return runCommand(ctx, model, "", append(args, "--prompt-file", file.Name())...)
}
