package pipeline

import (
	"sort"
	"strings"

	"esuna/internal/contract"
	"esuna/internal/reviewer"
)

// Merge parses raw reviewer results and avoids double-counting agreement.
// Models are visited in lexical order for deterministic output. Findings match
// when their file is identical, lines differ by at most two, and one message
// contains the other after case/whitespace normalization. The first location
// and message are retained, with the highest severity (P0 > P1 > P2).
// Running reviewers concurrently and requesting ReviewerOutputFormat belongs
// to the caller; Merge performs no CLI calls or confidence scoring.
func Merge(results map[reviewer.Model]string) ([]contract.Finding, error) {
	models := make([]reviewer.Model, 0, len(results))
	for model := range results {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i] < models[j] })
	findings := make([]contract.Finding, 0)
	for _, model := range models {
		parsed, err := ParseReviewerOutput(results[model])
		if err != nil {
			return nil, err
		}
		findings = mergeFindings(findings, parsed)
	}
	return findings, nil
}

func mergeFindings(findings, incoming []contract.Finding) []contract.Finding {
	for _, candidate := range incoming {
		matched := false
		for i, prior := range findings {
			// Subtract only in the larger-minus-smaller direction to avoid overflow.
			distance := prior.Line - candidate.Line
			if prior.Line < candidate.Line {
				distance = candidate.Line - prior.Line
			}
			if prior.File != candidate.File || distance > 2 {
				continue
			}
			a := strings.Join(strings.Fields(strings.ToLower(prior.Message)), " ")
			b := strings.Join(strings.Fields(strings.ToLower(candidate.Message)), " ")
			if !strings.Contains(a, b) && !strings.Contains(b, a) {
				continue
			}
			if candidate.Severity < prior.Severity {
				findings[i].Severity = candidate.Severity
			}
			matched = true
			break
		}
		if !matched {
			findings = append(findings, candidate)
		}
	}
	return findings
}
