package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base := strings.TrimRight(envOr("FORGE_MODELS_URL", "http://127.0.0.1:4300"), "/")
	embedModel := envOr("FORGE_MODELS_EMBED_MODEL", "local-embed-small")
	genModel := envOr("FORGE_MODELS_GEN_MODEL", "local-general")
	client := &http.Client{Timeout: 30 * time.Second}

	fmt.Println("== Demo 14 Go client ==")
	fmt.Printf("Models URL: %s\n", base)
	fmt.Printf("Embed model: %s\n", embedModel)
	fmt.Printf("Gen model: %s\n", genModel)

	dim, err := fetchEmbeddingDim(client, base, embedModel)
	if err != nil {
		return err
	}
	fmt.Printf("Registry embedding_dim=%d\n", dim)

	fmt.Println("[embed] POST /v1/models/" + embedModel + "/embed")
	embedBody, err := postJSON(client, base+"/v1/models/"+embedModel+"/embed", map[string]any{
		"input": "forge platform model serving demo",
	})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	var embedResp struct {
		Model      string      `json:"model"`
		Embeddings [][]float64 `json:"embeddings"`
		Dim        int         `json:"dim"`
	}
	if err := json.Unmarshal(embedBody, &embedResp); err != nil {
		return fmt.Errorf("embed decode: %w", err)
	}
	if embedResp.Dim != dim {
		return fmt.Errorf("embed response dim %d != registry dim %d", embedResp.Dim, dim)
	}
	if err := AssertEmbeddingDim(embedResp.Embeddings, dim); err != nil {
		return fmt.Errorf("embed assert: %w", err)
	}
	fmt.Printf("  PASS: embed vector length == %d\n", dim)

	fmt.Println("[classify] POST /v1/models/" + genModel + "/classify")
	classifyBody, err := postJSON(client, base+"/v1/models/"+genModel+"/classify", map[string]any{
		"input":  "database connection refused",
		"labels": []string{"network", "auth", "disk"},
	})
	if err != nil {
		return fmt.Errorf("classify: %w", err)
	}
	var classifyResp struct {
		Labels []LabelScore `json:"labels"`
	}
	if err := json.Unmarshal(classifyBody, &classifyResp); err != nil {
		return fmt.Errorf("classify decode: %w", err)
	}
	top, err := AssertTopLabel(classifyResp.Labels)
	if err != nil {
		return fmt.Errorf("classify assert: %w", err)
	}
	fmt.Printf("  PASS: top label=%q score=%.4f\n", top, classifyResp.Labels[0].Score)

	fmt.Println("[summarize] POST /v1/models/" + genModel + "/summarize")
	summaryBody, err := postJSON(client, base+"/v1/models/"+genModel+"/summarize", map[string]any{
		"input": "long incident text about database connection refused during deploy of forge platform services",
	})
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}
	var summaryResp struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(summaryBody, &summaryResp); err != nil {
		return fmt.Errorf("summarize decode: %w", err)
	}
	if err := AssertNonEmptySummary(summaryResp.Summary); err != nil {
		return fmt.Errorf("summarize assert: %w", err)
	}
	fmt.Printf("  PASS: summary=%q\n", truncate(summaryResp.Summary, 80))

	fmt.Println("client assertions PASSED")
	return nil
}

func fetchEmbeddingDim(client *http.Client, base, model string) (int, error) {
	resp, err := client.Get(base + "/v1/models/" + model)
	if err != nil {
		return 0, fmt.Errorf("get model: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("get model read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("get model: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var m struct {
		EmbeddingDim *int `json:"embedding_dim"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, fmt.Errorf("get model decode: %w", err)
	}
	if m.EmbeddingDim == nil || *m.EmbeddingDim <= 0 {
		return 0, fmt.Errorf("model %s missing positive embedding_dim", model)
	}
	return *m.EmbeddingDim, nil
}

func postJSON(client *http.Client, url string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
