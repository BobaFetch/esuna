package reviewer

import "context"

type codexRunner struct{}

func (codexRunner) Model() Model { return Codex }

func (codexRunner) Review(ctx context.Context, prompt string) (string, error) {
	// '-' selects stdin; normal exec stdout is the final response, not JSONL.
	return runCommand(ctx, Codex, prompt, "--ask-for-approval", "never", "exec", "--sandbox", "read-only", "--color", "never", "-")
}
