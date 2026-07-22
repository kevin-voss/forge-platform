// Package errmap classifies CLI failures into documented process exit codes.
package errmap

import (
	"context"
	"errors"
	"net"
	"net/http"

	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/control"
)

const (
	Success  = 0
	Generic  = 1
	Usage    = 2
	NotFound = 3
	Conflict = 4
	Timeout  = 5
)

// ExitCode maps a command error to the Forge CLI exit-code taxonomy.
func ExitCode(err error) int {
	if err == nil {
		return Success
	}

	var usageError *config.UsageError
	if errors.As(err, &usageError) {
		return Usage
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Timeout
	}

	var apiError *control.APIError
	if errors.As(err, &apiError) {
		switch apiError.Status {
		case http.StatusNotFound:
			return NotFound
		case http.StatusConflict:
			return Conflict
		}
	}

	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || control.IsNetworkError(err)) {
		return Timeout
	}
	if control.IsNetworkError(err) {
		return Timeout
	}
	return Generic
}
