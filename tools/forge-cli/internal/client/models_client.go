package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"forge.local/tools/forge-cli/internal/config"
)

const defaultModelsURL = "http://127.0.0.1:4300"

// ModelsClient is a thin HTTP client for forge-models.
type ModelsClient struct {
	http    *http.Client
	baseURL *url.URL
	verbose func(method, path string, status int, requestID string, duration time.Duration)
}

// ModelsAPIError is an error returned by forge-models.
type ModelsAPIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *ModelsAPIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// ModelInfo is a registry entry from GET /v1/models.
type ModelInfo struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Backend      string   `json:"backend"`
	EmbeddingDim *int     `json:"embedding_dim"`
	Status       string   `json:"status"`
}

// ModelListResponse is GET /v1/models.
type ModelListResponse struct {
	Models []ModelInfo `json:"models"`
}

// EmbedResponse is POST /v1/models/{model}/embed.
type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
	Dim        int         `json:"dim"`
	Usage      struct {
		InputCount int `json:"input_count"`
	} `json:"usage"`
}

// GenerateResponse is POST /v1/models/{model}/generate.
type GenerateResponse struct {
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
	Usage        struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// NewModelsClient creates a forge-models API client.
func NewModelsClient(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string, duration time.Duration)) (*ModelsClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultModelsURL()
	}
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse models endpoint: %w", err)
	}
	return &ModelsClient{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
		verbose: verbose,
	}, nil
}

// DefaultModelsURL returns FORGE_MODELS_URL or the local Compose default.
func DefaultModelsURL() string {
	if u := strings.TrimSpace(os.Getenv("FORGE_MODELS_URL")); u != "" {
		return u
	}
	return defaultModelsURL
}

// ListModels calls GET /v1/models.
func (c *ModelsClient) ListModels(ctx context.Context) (*ModelListResponse, error) {
	var out ModelListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/models", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Embed calls POST /v1/models/{model}/embed with a single text input.
func (c *ModelsClient) Embed(ctx context.Context, model, text string) (*EmbedResponse, error) {
	path := "/v1/models/" + url.PathEscape(model) + "/embed"
	var out EmbedResponse
	if err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"input": text}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Generate calls POST /v1/models/{model}/generate.
func (c *ModelsClient) Generate(ctx context.Context, model, prompt string, maxTokens int) (*GenerateResponse, error) {
	path := "/v1/models/" + url.PathEscape(model) + "/generate"
	body := map[string]any{"prompt": prompt, "temperature": 0}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	var out GenerateResponse
	if err := c.doJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *ModelsClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL.ResolveReference(&url.URL{Path: path}).String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("X-Request-ID")
	if c.verbose != nil {
		c.verbose(method, path, resp.StatusCode, requestID, time.Since(started))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		apiErr := &ModelsAPIError{Status: resp.StatusCode, RequestID: requestID, Message: strings.TrimSpace(string(raw))}
		var parsed struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			if parsed.Error != "" {
				apiErr.Message = parsed.Error
			}
			apiErr.Code = parsed.Code
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode models response: %w", err)
	}
	return nil
}
