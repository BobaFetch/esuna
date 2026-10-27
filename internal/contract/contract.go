// Package contract defines esuna's stable review output.
package contract

// Finding is a single review finding with calibrated confidence.
type Finding struct {
	File       string  `json:"file"`
	Line       int     `json:"line"`
	Severity   string  `json:"severity"` // "P0" | "P1" | "P2"
	Message    string  `json:"message"`
	Confidence float64 `json:"confidence"`
}

// ValidSeverity reports whether s is one of the supported severity labels.
func ValidSeverity(s string) bool {
	return s == "P0" || s == "P1" || s == "P2"
}

// Result is the JSON output contract for a review.
type Result struct {
	Summary           string    `json:"summary"`
	OverallConfidence float64   `json:"overall_confidence"`
	Findings          []Finding `json:"findings"`
	FilesReviewed     int       `json:"files_reviewed"`
	Base              string    `json:"base"`
}
