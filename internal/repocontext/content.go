package repocontext

import (
	"errors"
	"fmt"
	"os"
)

// FullContent reads current working-tree contents keyed by repo-relative path.
// Deleted files are skipped. Paths and symlinks must stay within repoRoot.
func FullContent(repoRoot string, files []string) (map[string]string, error) {
	root, err := os.OpenRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open repo root: %w", err)
	}
	defer root.Close()
	contents := make(map[string]string, len(files))
	for _, file := range files {
		data, err := root.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read touched file %q: %w", file, err)
		}
		contents[file] = string(data)
	}
	return contents, nil
}
