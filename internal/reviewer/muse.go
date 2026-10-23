package reviewer

import "context"

type museRunner struct{}

func (museRunner) Model() Model { return Muse }

func (museRunner) Review(ctx context.Context, prompt string) (string, error) {
	return runPromptFile(ctx, Muse, prompt, "exec", "--disable-write", "--approval-mode", "never")
}
