package reviewer

import (
	"reflect"
	"testing"
)

func TestBuildPool(t *testing.T) {
	tests := []struct {
		author Model
		want   []Model
	}{
		{"", []Model{Claude, Codex, Muse, Grok}},
		{Claude, []Model{Codex, Muse, Grok}},
		{Codex, []Model{Claude, Muse, Grok}},
		{Muse, []Model{Claude, Codex, Grok}},
		{Grok, []Model{Claude, Codex, Muse}},
	}
	for _, tt := range tests {
		t.Run("exclude="+string(tt.author), func(t *testing.T) {
			pool, err := BuildPool(tt.author)
			if err != nil {
				t.Fatal(err)
			}
			var got []Model
			for _, runner := range pool {
				got = append(got, runner.Model())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("models = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildPoolUnknownAuthor(t *testing.T) {
	for _, author := range []Model{"unknown", "Claude", " claude"} {
		if pool, err := BuildPool(author); err == nil || pool != nil {
			t.Fatalf("BuildPool(%q) = %v, %v; want nil pool and error", author, pool, err)
		}
	}
}

func TestBuildPoolDoesNotRequirePATH(t *testing.T) {
	t.Setenv("PATH", "")
	pool, err := BuildPool("")
	if err != nil || len(pool) != 4 {
		t.Fatalf("pool = %v, err = %v", pool, err)
	}
}
