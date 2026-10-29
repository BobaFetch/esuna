// Package pipeline prepares draft findings before confidence scoring.
package pipeline

import (
	"path/filepath"
	"strconv"
	"strings"

	"esuna/internal/contract"
)

// ReviewerOutputFormat is the shared instruction for independent and chained reviews.
const ReviewerOutputFormat = "Reply only with one finding per line as FILE|LINE|SEVERITY|MESSAGE. FILE must be a repo-relative path without a pipe or newline; LINE a positive 1-based integer; SEVERITY P0, P1, or P2; MESSAGE nonempty single-line text (pipes allowed). Do not include confidence, headings, or code fences. If there are no findings, return empty output."

// ParseReviewerOutput parses FILE|LINE|SEVERITY|MESSAGE records, one per line.
// Fields are whitespace-trimmed; only the first three pipes are delimiters.
// FILE must be repo-relative (no parent escape), LINE positive and 1-based,
// SEVERITY exactly P0/P1/P2, and MESSAGE nonempty. Malformed lines and prose
// are skipped, including in mixed output; empty output yields an empty slice.
// Draft findings have Confidence zero. There are currently no fatal parse errors.
func ParseReviewerOutput(raw string) ([]contract.Finding, error) {
	findings := make([]contract.Finding, 0)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.SplitN(line, "|", 4)
		if len(fields) != 4 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if len(fields[0]) > 255 || strings.ContainsAny(fields[0], "\n\r	") || strings.Contains(fields[0], "  ") {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 || !filepath.IsLocal(fields[0]) || !contract.ValidSeverity(fields[2]) || fields[3] == "" {
			continue
		}
		findings = append(findings, contract.Finding{File: filepath.Clean(fields[0]), Line: n, Severity: fields[2], Message: fields[3]})
	}
	return findings, nil
}
