package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelListEmbedGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{
						"id":           "local-embed-small",
						"capabilities": []string{"embed"},
						"backend":      "local",
						"embedding_dim": 384,
						"status":       "ok",
					},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/embed"):
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "hello") {
				t.Fatalf("unexpected embed body: %s", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":      "local-embed-small",
				"embeddings": [][]float64{{0.1, 0.2}},
				"dim":        2,
				"usage":      map[string]any{"input_count": 1},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/generate"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"text":          "[forge-gen] hi",
				"finish_reason": "stop",
				"usage": map[string]any{
					"prompt_tokens":     1,
					"completion_tokens": 2,
					"total_tokens":      3,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FORGE_MODELS_URL", server.URL)

	root := NewRootCommand("test")
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"model", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("model list: %v", err)
	}
	if !strings.Contains(out.String(), "local-embed-small") {
		t.Fatalf("list output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetArgs([]string{"model", "embed", "--model", "local-embed-small", "--text", "hello"})
	if err := root.Execute(); err != nil {
		t.Fatalf("model embed: %v", err)
	}
	if !strings.Contains(out.String(), "dim=2") {
		t.Fatalf("embed output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetArgs([]string{"model", "generate", "--model", "local-general", "--prompt", "hi"})
	if err := root.Execute(); err != nil {
		t.Fatalf("model generate: %v", err)
	}
	if !strings.Contains(out.String(), "[forge-gen] hi") {
		t.Fatalf("generate output = %q", out.String())
	}
}

func TestModelEmbedRequiresFlags(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"model", "embed", "--model", "m"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--text") {
		t.Fatalf("error = %v, want --text required", err)
	}
}
