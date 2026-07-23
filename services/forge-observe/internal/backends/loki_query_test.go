package backends_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"forge.local/services/forge-observe/internal/backends"
)

func TestLokiQueryRange(t *testing.T) {
	ts := time.Unix(0, 1721736000000000000).UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Fatal("missing query")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "streams",
				"result": []any{
					map[string]any{
						"stream": map[string]string{"forge_project": "prj_1", "forge_service": "control"},
						"values": [][]string{
							{strconv.FormatInt(ts.UnixNano(), 10), `{"message":"hi","trace_id":"T","service":"control","level":"info"}`},
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := backends.NewLoki(srv.URL, backends.Options{Timeout: time.Second})
	out, err := c.QueryRange(context.Background(), `{forge_project="prj_1"} | json`, ts.Add(-time.Minute), ts.Add(time.Minute), 10, "forward")
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(out) != 1 || out[0].Labels["forge_project"] != "prj_1" {
		t.Fatalf("out = %+v", out)
	}
}

func TestLokiQueryRangeErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(srv.Close)
	c := backends.NewLoki(srv.URL, backends.Options{Timeout: time.Second})
	_, err := c.QueryRange(context.Background(), `{job=~".+"}`, time.Now().Add(-time.Hour), time.Now(), 10, "backward")
	if err == nil {
		t.Fatal("expected error")
	}
}
