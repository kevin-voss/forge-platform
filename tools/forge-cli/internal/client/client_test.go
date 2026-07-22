package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewAppliesTimeoutAndCapturesRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Request-Id", "request-123")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	forgeClient, err := New(server.URL, 125*time.Millisecond)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if forgeClient.HTTP.Timeout != 125*time.Millisecond {
		t.Fatalf("timeout = %s, want 125ms", forgeClient.HTTP.Timeout)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := forgeClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	if response.RequestID != "request-123" {
		t.Fatalf("request ID = %q, want request-123", response.RequestID)
	}
}
