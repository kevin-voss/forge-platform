package errmap

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/auth"
	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/control"
)

func TestExitCodeTaxonomy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", want: Success},
		{name: "generic", err: fmt.Errorf("unexpected"), want: Generic},
		{name: "usage", err: &config.UsageError{Message: "bad flag"}, want: Usage},
		{name: "not found", err: &control.APIError{Status: http.StatusNotFound}, want: NotFound},
		{name: "conflict", err: &control.APIError{Status: http.StatusConflict}, want: Conflict},
		{name: "unauthorized", err: &control.APIError{Status: http.StatusUnauthorized}, want: Auth},
		{name: "forbidden", err: &control.APIError{Status: http.StatusForbidden}, want: Auth},
		{name: "secrets unauthorized", err: &sharedclient.SecretsAPIError{Status: http.StatusUnauthorized}, want: Auth},
		{name: "secrets forbidden", err: &sharedclient.SecretsAPIError{Status: http.StatusForbidden}, want: Auth},
		{name: "secrets not found", err: &sharedclient.SecretsAPIError{Status: http.StatusNotFound}, want: NotFound},
		{name: "auth local", err: &auth.Error{Message: "session expired, run forge login"}, want: Auth},
		{name: "deadline", err: fmt.Errorf("request Control: %w", context.DeadlineExceeded), want: Timeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExitCode(test.err); got != test.want {
				t.Fatalf("ExitCode(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

func TestAuthErrorMessages(t *testing.T) {
	unauthorized := &control.APIError{Status: http.StatusUnauthorized, Message: "inactive or unknown token"}
	if got := unauthorized.Error(); got != "not logged in or session expired; run forge login" {
		t.Fatalf("401 message = %q", got)
	}

	forbidden := &control.APIError{
		Status:  http.StatusForbidden,
		Message: "forbidden",
		Details: map[string]string{"required_action": "deployment.create", "role": "viewer"},
	}
	if got := forbidden.Error(); !strings.Contains(got, "deployment.create") || !strings.Contains(got, "viewer") {
		t.Fatalf("403 message = %q", got)
	}
}
