package repocontext

// Bundle contains the working-tree context consumed by reviewer prompt builders.
type Bundle struct {
	Diff    string
	Files   map[string]string
	Symbols map[string][]Symbol
}

// Build gathers context against base in repoRoot, without changing the process's
// working directory. Only deep builds invoke ctags and populate Symbols.
func Build(repoRoot, base string, deep bool) (*Bundle, error) {
	diff, err := diffAt(repoRoot, base, false)
	if err != nil {
		return nil, err
	}
	files, err := touchedFilesAt(repoRoot, base)
	if err != nil {
		return nil, err
	}
	contents, err := FullContent(repoRoot, files)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{Diff: diff, Files: contents}
	if deep {
		bundle.Symbols, err = SymbolMap(repoRoot)
		if err != nil {
			return nil, err
		}
	}
	return bundle, nil
}
