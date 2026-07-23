// Package errmap classifies CLI failures into documented process exit codes.
package errmap

import (
	"context"
	"errors"
	"net"
	"net/http"

	"forge.local/tools/forge-cli/internal/auth"
	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/control"
	"forge.local/tools/forge-cli/internal/identity"
)

const (
	Success  = 0
	Generic  = 1
	Usage    = 2
	NotFound = 3
	// Auth covers HTTP 401/403. Conflict remains 4 for HTTP 409 (same numeric code).
	Auth     = 4
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
	var authError *auth.Error
	if errors.As(err, &authError) {
		return Auth
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Timeout
	}

	var apiError *control.APIError
	if errors.As(err, &apiError) {
		switch apiError.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Auth
		case http.StatusNotFound:
			return NotFound
		case http.StatusConflict:
			return Conflict
		}
	}

	var identityError *identity.APIError
	if errors.As(err, &identityError) {
		switch identityError.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Auth
		}
	}

	var secretsError *sharedclient.SecretsAPIError
	if errors.As(err, &secretsError) {
		switch secretsError.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Auth
		case http.StatusNotFound:
			return NotFound
		case http.StatusConflict:
			return Conflict
		}
	}

	var observeError *sharedclient.ObserveAPIError
	if errors.As(err, &observeError) {
		switch observeError.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Auth
		case http.StatusNotFound:
			return NotFound
		}
	}

	var modelsError *sharedclient.ModelsAPIError
	if errors.As(err, &modelsError) {
		switch modelsError.Status {
		case http.StatusNotFound:
			return NotFound
		}
	}

	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || control.IsNetworkError(err)) {
		return Timeout
	}
	if control.IsNetworkError(err) || identity.IsNetworkError(err) || sharedclient.IsSecretsNetworkError(err) {
		return Timeout
	}
	return Generic
}
