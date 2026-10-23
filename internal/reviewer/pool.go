package reviewer

import "fmt"

// BuildPool returns all four runners except excludeAuthor (if non-empty).
// It does not probe PATH: missing binaries are reported by Review, not silently
// removed. Author identifiers are matched literally, without inference.
func BuildPool(excludeAuthor Model) ([]Runner, error) {
	switch excludeAuthor {
	case "", Claude, Codex, Muse, Grok:
	default:
		return nil, fmt.Errorf("unrecognized author model %q: expected claude, codex, muse, or grok", excludeAuthor)
	}
	var pool []Runner
	for _, runner := range []Runner{claudeRunner{}, codexRunner{}, museRunner{}, grokRunner{}} {
		if runner.Model() != excludeAuthor {
			pool = append(pool, runner)
		}
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("reviewer pool is empty after excluding author %q", excludeAuthor)
	}
	return pool, nil
}
