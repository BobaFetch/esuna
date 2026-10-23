package reviewer

import "context"

type grokRunner struct{}

func (grokRunner) Model() Model { return Grok }

func (grokRunner) Review(ctx context.Context, prompt string) (string, error) {
	// --prompt-file selects single-turn headless mode on the installed CLI.
	return runPromptFile(ctx, Grok, prompt, "--no-auto-update", "--permission-mode", "plan", "--output-format", "plain")
}
