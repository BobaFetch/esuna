package reviewer

import "context"

type claudeRunner struct{}

func (claudeRunner) Model() Model { return Claude }

func (claudeRunner) Review(ctx context.Context, prompt string) (string, error) {
	// Print mode accepts piped text, avoiding argv limits for large diffs.
	return runCommand(ctx, Claude, prompt, "-p", "--output-format", "text", "--permission-mode", "plan", "--permission-prompts", "none")
}
