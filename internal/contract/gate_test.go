package contract_test

import (
	"testing"

	"esuna/internal/contract"
)

func TestDefaultGateThreshold(t *testing.T) {
	if contract.DefaultGateThreshold != 0.7 {
		t.Fatalf("DefaultGateThreshold = %v; want 0.7", contract.DefaultGateThreshold)
	}
}

func TestGated(t *testing.T) {
	tests := []struct {
		name      string
		findings  []contract.Finding
		threshold float64
		want      bool
	}{
		{"nil findings", nil, 0.7, false},
		{"empty findings", []contract.Finding{}, 0.7, false},
		{"no P0", []contract.Finding{{Severity: "P1", Confidence: 1}, {Severity: "P2", Confidence: 1}}, 0.7, false},
		{"P0 below threshold", []contract.Finding{{Severity: "P0", Confidence: 0.699}}, 0.7, false},
		{"P0 at threshold", []contract.Finding{{Severity: "P0", Confidence: 0.7}}, 0.7, true},
		{"P0 above threshold", []contract.Finding{{Severity: "P0", Confidence: 0.8}}, 0.7, true},
		{"custom threshold below", []contract.Finding{{Severity: "P0", Confidence: 0.8}}, 0.9, false},
		{"custom threshold at", []contract.Finding{{Severity: "P0", Confidence: 0.9}}, 0.9, true},
		{"lower custom threshold", []contract.Finding{{Severity: "P0", Confidence: 0.5}}, 0.4, true},
		{"later P0 gates", []contract.Finding{{Severity: "P1", Confidence: 1}, {Severity: "P0", Confidence: 0.2}, {Severity: "P0", Confidence: 0.9}}, 0.7, true},
		{"invalid severity", []contract.Finding{{Severity: "p0", Confidence: 1}, {Severity: "", Confidence: 1}}, 0.7, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contract.Gated(tt.findings, tt.threshold); got != tt.want {
				t.Errorf("Gated(%v, %v) = %v; want %v", tt.findings, tt.threshold, got, tt.want)
			}
		})
	}
}
