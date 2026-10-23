package pipeline

import (
	"reflect"
	"strings"
	"testing"

	"esuna/internal/contract"
)

func TestParseReviewerOutput(t *testing.T) {
	tests := []struct {
		name, raw string
		want      []contract.Finding
	}{
		{"valid", " a.go | 12 | P1 | nil pointer | dereference\r\nb.go|1|P0|data loss", []contract.Finding{{File: "a.go", Line: 12, Severity: "P1", Message: "nil pointer | dereference"}, {File: "b.go", Line: 1, Severity: "P0", Message: "data loss"}}},
		{"malformed skipped", "prose\n```\na.go|0|P1|bad\na.go|-1|P1|bad\na.go|x|P1|bad\na.go|1|P3|bad\n|1|P1|bad\na.go|1|P1| \n../outside|1|P1|bad\n/absolute|1|P1|bad\na.go|2|P2|valid", []contract.Finding{{File: "a.go", Line: 2, Severity: "P2", Message: "valid"}}},
		{"empty", " \n", []contract.Finding{}},
		{"long message", "a.go|1|P2|" + strings.Repeat("x", 100000), []contract.Finding{{File: "a.go", Line: 1, Severity: "P2", Message: strings.Repeat("x", 100000)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReviewerOutput(tt.raw)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
}
