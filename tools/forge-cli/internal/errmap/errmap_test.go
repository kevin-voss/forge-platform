package errmap

import (
	"context"
	"fmt"
	"net/http"
	"testing"

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
