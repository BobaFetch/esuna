package repocontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func put(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func changedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init")
	put(t, root, "edit.txt", "before\n")
	put(t, root, "deleted.txt", "gone\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	put(t, root, "edit.txt", "after\n")
	put(t, root, "space\nname.txt", "staged\n")
	gitRun(t, root, "add", "space\nname.txt")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBuildUsesRepoRootAndDeepFlag(t *testing.T) {
	root := changedRepo(t)
	// Do not chdir: Build must target repoRoot, not the caller's repo.
	shallow, err := Build(root, "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if shallow.Symbols != nil || shallow.Files["edit.txt"] != "after\n" || !strings.Contains(shallow.Diff, "+after") {
		t.Fatalf("shallow bundle = %#v", shallow)
	}
	t.Run("deep failure is not suppressed", func(t *testing.T) {
		originalPath := os.Getenv("PATH")
		fakeCtags(t, "exit 1\n")
		t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+originalPath)
		if _, err := Build(root, "HEAD", false); err != nil {
			t.Fatalf("shallow build invoked ctags: %v", err)
		}
		if _, err := Build(root, "HEAD", true); err == nil {
			t.Fatal("deep build suppressed ctags failure")
		}
	})
	requireUniversalCtags(t)
	put(t, root, "untouched.go", "package fixture\nfunc RepoWideSymbol() {}\n")
	deep, err := Build(root, "HEAD", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(deep.Symbols["untouched.go"]) == 0 {
		t.Fatalf("deep symbols = %#v", deep.Symbols)
	}
	if _, ok := deep.Files["untouched.go"]; ok {
		t.Fatal("untracked file included in touched content")
	}
}

func TestDiffTouchedFilesAndContent(t *testing.T) {
	root := changedRepo(t)
	t.Chdir(root)
	diff, err := Diff("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-before", "+after", "+staged", "-gone"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q: %s", want, diff)
		}
	}
	files, err := TouchedFiles("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"deleted.txt", "edit.txt", "space\nname.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	contents, err := FullContent(root, files)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"edit.txt": "after\n", "space\nname.txt": "staged\n"}
	if !reflect.DeepEqual(contents, want) {
		t.Fatalf("contents = %#v, want %#v", contents, want)
	}
	if _, err := Diff("no-such-ref"); err == nil {
		t.Error("invalid ref succeeded")
	}
	if _, err := TouchedFiles("--output=oops"); err == nil {
		t.Error("option-like ref succeeded")
	}
	if _, err := FullContent(root, []string{"."}); err == nil {
		t.Error("reading directory succeeded")
	}
}
