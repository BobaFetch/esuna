package repocontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireUniversalCtags(t *testing.T) {
	t.Helper()
	bin, err := exec.LookPath("ctags")
	if err != nil {
		t.Skip("ctags not on PATH")
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Universal Ctags") {
		t.Skipf("%s is not a working Universal Ctags; install universal-ctags and put it first on PATH (version error: %v; output: %s)", bin, err, strings.TrimSpace(string(out)))
	}
}

func TestSymbolMapReal(t *testing.T) {
	requireUniversalCtags(t)
	root := t.TempDir()
	put(t, root, "fixture.go", "package fixture\nfunc KnownFunction() {}\n")
	symbols, err := SymbolMap(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols["fixture.go"] {
		if symbol.Name == "KnownFunction" && symbol.Kind == "func" && symbol.Line == 2 {
			return
		}
	}
	t.Fatalf("missing function: %#v", symbols)
}

func fakeCtags(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	put(t, bin, "ctags", "#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'Universal Ctags\\n'; exit 0; fi\n"+body)
	if err := exec.Command("chmod", "+x", filepath.Join(bin, "ctags")).Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestSymbolMapDeduplicates(t *testing.T) {
	fakeCtags(t, "printf '%s\\n' '{\"_type\":\"ptag\",\"name\":\"metadata\"}' '{\"_type\":\"tag\",\"path\":\"./fixture.go\",\"name\":\"Known\",\"kind\":\"func\",\"line\":2}' '{\"_type\":\"tag\",\"path\":\"fixture.go\",\"name\":\"Known\",\"kind\":\"func\",\"line\":2}'\n")
	symbols, err := SymbolMap(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 || len(symbols["fixture.go"]) != 1 {
		t.Fatalf("symbols = %#v", symbols)
	}
}

func TestSymbolMapErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := SymbolMap(t.TempDir()); err == nil || !strings.Contains(err.Error(), "universal-ctags not found on PATH") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("legacy", func(t *testing.T) {
		fakeCtags(t, "exit 0\n")
		// Replace the executable's version response to represent a legacy install.
		put(t, filepath.SplitList(os.Getenv("PATH"))[0], "ctags", "#!/bin/sh\nprintf 'Exuberant Ctags\\n'\n")
		if _, err := SymbolMap(t.TempDir()); err == nil || !strings.Contains(err.Error(), "Universal Ctags") {
			t.Fatalf("error = %v", err)
		}
	})
	for name, body := range map[string]string{"malformed": "printf 'not-json\\n'\n", "failed": "printf 'ctags failed' >&2; exit 1\n"} {
		t.Run(name, func(t *testing.T) {
			fakeCtags(t, body)
			if _, err := SymbolMap(t.TempDir()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
