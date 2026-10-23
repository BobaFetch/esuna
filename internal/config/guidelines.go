package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadGuidelines returns optional review guidance verbatim; a missing file is empty.
func LoadGuidelines(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".esuna", "guidelines.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read .esuna/guidelines.md: %w", err)
	}
	return string(data), nil
}
