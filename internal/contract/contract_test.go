package contract_test

import (
	"encoding/json"
	"testing"

	"esuna/internal/contract"
)

func TestValidSeverity(t *testing.T) {
	for _, s := range []string{"P0", "P1", "P2"} {
		if !contract.ValidSeverity(s) {
			t.Errorf("ValidSeverity(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"", "P3", "p0", " P0", "P1 ", "critical"} {
		if contract.ValidSeverity(s) {
			t.Errorf("ValidSeverity(%q) = true; want false", s)
		}
	}
}

func TestResultJSON(t *testing.T) {
	result := contract.Result{
		Summary:           "One finding",
		OverallConfidence: 0.85,
		Findings: []contract.Finding{{
			File:       "main.go",
			Line:       12,
			Severity:   "P0",
			Message:    "Handle the error",
			Confidence: 0.9,
		}},
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"summary":"One finding","overall_confidence":0.85,"findings":[{"file":"main.go","line":12,"severity":"P0","message":"Handle the error","confidence":0.9}]}`
	if string(got) != want {
		t.Fatalf("Result JSON = %s; want %s", got, want)
	}
}
