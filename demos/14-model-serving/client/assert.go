package main

import (
	"fmt"
	"math"
	"strings"
)

// LabelScore is one scored classification label (sorted descending by score).
type LabelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// AssertEmbeddingDim checks that every embedding vector has length equal to dim.
func AssertEmbeddingDim(embeddings [][]float64, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("expected positive dim, got %d", dim)
	}
	if len(embeddings) == 0 {
		return fmt.Errorf("expected at least one embedding")
	}
	for i, vec := range embeddings {
		if len(vec) != dim {
			return fmt.Errorf("embedding[%d]: length %d != model dim %d", i, len(vec), dim)
		}
	}
	return nil
}

// AssertTopLabel requires a non-empty label list sorted by score descending
// and returns the top label.
func AssertTopLabel(labels []LabelScore) (string, error) {
	if len(labels) == 0 {
		return "", fmt.Errorf("expected at least one classification label")
	}
	for i, ls := range labels {
		if strings.TrimSpace(ls.Label) == "" {
			return "", fmt.Errorf("labels[%d]: empty label", i)
		}
		if math.IsNaN(ls.Score) || math.IsInf(ls.Score, 0) {
			return "", fmt.Errorf("labels[%d]: invalid score %v", i, ls.Score)
		}
		if i > 0 && labels[i-1].Score < ls.Score {
			return "", fmt.Errorf(
				"labels not sorted descending: labels[%d].score=%v < labels[%d].score=%v",
				i-1, labels[i-1].Score, i, ls.Score,
			)
		}
	}
	return labels[0].Label, nil
}

// AssertNonEmptySummary requires a non-empty trimmed summary string.
func AssertNonEmptySummary(summary string) error {
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("expected non-empty summary")
	}
	return nil
}
