package contract

// DefaultGateThreshold is the default minimum confidence for a blocking P0 finding.
const DefaultGateThreshold = 0.7

// Gated reports whether any P0 finding has confidence at or above threshold.
func Gated(findings []Finding, threshold float64) bool {
	for _, finding := range findings {
		if finding.Severity == "P0" && finding.Confidence >= threshold {
			return true
		}
	}
	return false
}
