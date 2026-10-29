//go:build ignore

// repomap regenerates docs/map/REPOMAP.md from the Go source: the package
// import graph, per-file declarations, cross-package symbol usage, and exported
// symbols nothing else uses. Stdlib only; run from the repository root:
//
//	go run scripts/repomap.go          # rewrite docs/map/REPOMAP.md
//	go run scripts/repomap.go -check   # exit 1 if the committed map is stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const outPath = "docs/map/REPOMAP.md"

type decl struct {
	Name, Kind string // Kind: func, method, type, const, var
	Recv       string
	Line       int
}

type file struct {
	Path            string
	Pkg             string // import path
	Test            bool
	Lines           int
	Decls           []decl
	Uses            map[string]bool // "pkg.Symbol" referenced from this file
	Idents          map[string]bool // every identifier name used in this file
	InternalImports []string
}

func main() {
	check := flag.Bool("check", false, "exit 1 if "+outPath+" is out of date")
	flag.Parse()
	module, err := modulePath()
	if err != nil {
		fail(err)
	}
	files, err := load(module)
	if err != nil {
		fail(err)
	}
	out := render(module, files)
	if *check {
		current, err := os.ReadFile(outPath)
		if err != nil || !bytes.Equal(current, out) {
			fmt.Fprintf(os.Stderr, "%s is stale; run: go run scripts/repomap.go\n", outPath)
			os.Exit(1)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(outPath, out, 0644); err != nil {
		fail(err)
	}
	fmt.Println("wrote", outPath)
}

func fail(err error) { fmt.Fprintln(os.Stderr, "repomap:", err); os.Exit(1) }

func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("run from the repository root: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module line in go.mod")
}

func load(module string) ([]*file, error) {
	var files []*file
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "scripts" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		af, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		pkg := module
		if dir != "." {
			pkg = module + "/" + dir
		}
		f := &file{Path: filepath.ToSlash(p), Pkg: pkg, Test: strings.HasSuffix(p, "_test.go"),
			Lines: bytes.Count(src, []byte("\n")), Uses: map[string]bool{}, Idents: map[string]bool{}}
		aliases := map[string]string{} // local name -> import path
		for _, imp := range af.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			if ip != module && !strings.HasPrefix(ip, module+"/") {
				continue
			}
			name := path.Base(ip)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			aliases[name] = ip
			f.InternalImports = append(f.InternalImports, ip)
		}
		for _, d := range af.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				k, recv := "func", ""
				if d.Recv != nil && len(d.Recv.List) > 0 {
					k, recv = "method", typeName(d.Recv.List[0].Type)
				}
				f.Decls = append(f.Decls, decl{d.Name.Name, k, recv, fset.Position(d.Pos()).Line})
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						f.Decls = append(f.Decls, decl{s.Name.Name, "type", "", fset.Position(s.Pos()).Line})
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.Name != "_" {
								f.Decls = append(f.Decls, decl{n.Name, d.Tok.String(), "", fset.Position(n.Pos()).Line})
							}
						}
					}
				}
			}
		}
		// Idents holds bare identifier uses. Selector right-hand sides (x.Field)
		// and struct-literal keys (T{Field: v}) are skipped so field names that
		// shadow a package-level func do not count as uses of it.
		var visit func(ast.Node) bool
		visit = func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok {
					if ip, ok := aliases[x.Name]; ok {
						f.Uses[ip+"."+n.Sel.Name] = true
						return false
					}
				}
				ast.Inspect(n.X, visit)
				return false
			case *ast.KeyValueExpr:
				if _, ok := n.Key.(*ast.Ident); ok {
					ast.Inspect(n.Value, visit)
					return false
				}
			case *ast.Field: // struct fields and params: names are declarations
				ast.Inspect(n.Type, visit)
				return false
			case *ast.Ident:
				f.Idents[n.Name] = true
			}
			return true
		}
		ast.Inspect(af, visit)
		files = append(files, f)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return typeName(t.X)
	}
	return "?"
}

func short(module, ip string) string {
	if ip == module {
		return "(root)"
	}
	return strings.TrimPrefix(ip, module+"/")
}

func mermaidID(s string) string {
	return strings.NewReplacer("/", "_", "(", "", ")", "", ".", "_").Replace(s)
}

func render(module string, files []*file) []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	pkgs := map[string][]*file{}
	for _, f := range files {
		pkgs[f.Pkg] = append(pkgs[f.Pkg], f)
	}
	names := make([]string, 0, len(pkgs))
	for p := range pkgs {
		names = append(names, p)
	}
	sort.Strings(names)

	w("<!-- Code generated by scripts/repomap.go; DO NOT EDIT. Regenerate: go run scripts/repomap.go -->\n\n")
	w("# %s repo map (generated)\n\n", module)
	w("Structural facts derived from source. For intent, data flow, and invariants see [ARCHITECTURE.md](../ARCHITECTURE.md).\n\n")

	// Package import graph (production code only).
	w("## Package import graph\n\nProduction imports only (tests excluded). Arrows point from importer to imported package.\n\n```mermaid\nflowchart TD\n")
	edges := map[string]bool{}
	for _, p := range names {
		w("  %s[\"%s\"]\n", mermaidID(short(module, p)), short(module, p))
		for _, f := range pkgs[p] {
			if f.Test {
				continue
			}
			for _, ip := range f.InternalImports {
				edges[fmt.Sprintf("  %s --> %s\n", mermaidID(short(module, p)), mermaidID(short(module, ip)))] = true
			}
		}
	}
	edgeList := make([]string, 0, len(edges))
	for e := range edges {
		edgeList = append(edgeList, e)
	}
	sort.Strings(edgeList)
	for _, e := range edgeList {
		w("%s", e)
	}
	w("```\n\n")

	// Size table.
	w("## Packages\n\n| Package | Files | Prod lines | Test lines | Imported by (prod) |\n|---|---:|---:|---:|---|\n")
	for _, p := range names {
		var n, prod, test int
		for _, f := range pkgs[p] {
			n++
			if f.Test {
				test += f.Lines
			} else {
				prod += f.Lines
			}
		}
		var importers []string
		for _, q := range names {
			if q == p {
				continue
			}
			for _, f := range pkgs[q] {
				if !f.Test && contains(f.InternalImports, p) {
					importers = append(importers, "`"+short(module, q)+"`")
					break
				}
			}
		}
		by := strings.Join(importers, ", ")
		if by == "" {
			by = "—"
		}
		w("| `%s` | %d | %d | %d | %s |\n", short(module, p), n, prod, test, by)
	}
	w("\n")

	// Cross-package symbol usage.
	w("## Cross-package symbol usage\n\nWhich production files use which symbols from other packages in this module.\n\n")
	for _, f := range files {
		if f.Test || len(f.Uses) == 0 {
			continue
		}
		uses := make([]string, 0, len(f.Uses))
		for u := range f.Uses {
			dot := strings.LastIndex(u, ".")
			uses = append(uses, "`"+short(module, u[:dot])+"."+u[dot+1:]+"`")
		}
		sort.Strings(uses)
		w("- `%s` → %s\n", f.Path, strings.Join(uses, ", "))
	}
	w("\n")

	// Declarations per file.
	w("## Declarations by file\n\n")
	for _, p := range names {
		w("### `%s`\n\n", short(module, p))
		for _, f := range pkgs[p] {
			if f.Test {
				continue
			}
			w("- **`%s`** (%d lines)\n", f.Path, f.Lines)
			for _, d := range f.Decls {
				label := d.Name
				if d.Kind == "method" {
					label = "(" + d.Recv + ")." + d.Name
				}
				w("  - %s `%s` — L%d\n", d.Kind, label, d.Line)
			}
		}
		var tests []string
		for _, f := range pkgs[p] {
			if f.Test {
				tests = append(tests, "`"+path.Base(f.Path)+"`")
			}
		}
		if len(tests) > 0 {
			w("- tests: %s\n", strings.Join(tests, ", "))
		}
		w("\n")
	}

	// Exported symbols with no production use outside their declaring file.
	w("## Exported but unused in production code\n\nExported identifiers not referenced by any non-test file other than the one declaring them. Candidates for unexporting or deletion; may be intentional test seams. Name-based, so treat as a hint.\n\n")
	found := false
	for _, p := range names {
		for _, f := range pkgs[p] {
			if f.Test || strings.HasPrefix(short(module, p), "cmd/") || p == module {
				continue
			}
			for _, d := range f.Decls {
				if !ast.IsExported(d.Name) || d.Kind == "method" {
					continue
				}
				used := false
				for _, g := range files {
					if g.Test || g == f {
						continue
					}
					if g.Uses[p+"."+d.Name] || (g.Pkg == p && g.Idents[d.Name]) {
						used = true
						break
					}
				}
				if !used && !usedLaterInSameFile(f, d) {
					w("- `%s.%s` (%s, `%s` L%d)\n", short(module, p), d.Name, d.Kind, f.Path, d.Line)
					found = true
				}
			}
		}
	}
	if !found {
		w("None.\n")
	}
	return b.Bytes()
}

// usedLaterInSameFile is a coarse check: the name is referenced in its own file
// beyond the declaration itself. Ident sets cannot count occurrences, so this
// re-reads the file and counts word-boundary matches.
func usedLaterInSameFile(f *file, d decl) bool {
	src, err := os.ReadFile(f.Path)
	if err != nil {
		return false
	}
	count := 0
	s := string(src)
	for i := 0; ; {
		j := strings.Index(s[i:], d.Name)
		if j < 0 {
			break
		}
		start, end := i+j, i+j+len(d.Name)
		if (start == 0 || !isIdent(s[start-1])) && (end == len(s) || !isIdent(s[end])) {
			count++
		}
		i = end
	}
	return count > 1+commentMentions(s, d.Name)
}

func commentMentions(s, name string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			n += strings.Count(t, name)
		}
	}
	return n
}

func isIdent(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
