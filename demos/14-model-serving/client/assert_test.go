package main

import "testing"

func TestAssertEmbeddingDim(t *testing.T) {
	t.Parallel()
	if err := AssertEmbeddingDim([][]float64{{0.1, 0.2, 0.3}}, 3); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := AssertEmbeddingDim([][]float64{{0.1, 0.2}}, 3); err == nil {
		t.Fatal("expected length mismatch error")
	}
	if err := AssertEmbeddingDim(nil, 3); err == nil {
		t.Fatal("expected empty embeddings error")
	}
	if err := AssertEmbeddingDim([][]float64{{1}}, 0); err == nil {
		t.Fatal("expected non-positive dim error")
	}
}

func TestAssertTopLabel(t *testing.T) {
	t.Parallel()
	top, err := AssertTopLabel([]LabelScore{
		{Label: "network", Score: 0.9},
		{Label: "auth", Score: 0.4},
	})
	if err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if top != "network" {
		t.Fatalf("top=%q want network", top)
	}
	if _, err := AssertTopLabel(nil); err == nil {
		t.Fatal("expected empty labels error")
	}
	if _, err := AssertTopLabel([]LabelScore{
		{Label: "a", Score: 0.1},
		{Label: "b", Score: 0.9},
	}); err == nil {
		t.Fatal("expected unsorted error")
	}
	if _, err := AssertTopLabel([]LabelScore{{Label: "  ", Score: 1}}); err == nil {
		t.Fatal("expected empty label error")
	}
}

func TestAssertNonEmptySummary(t *testing.T) {
	t.Parallel()
	if err := AssertNonEmptySummary("[forge-summary] hello"); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := AssertNonEmptySummary("   \n"); err == nil {
		t.Fatal("expected empty summary error")
	}
}
