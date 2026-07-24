package main

import (
	"errors"
	"os"
	"strings"
)

type config struct {
	// ProductAuth is "enforce" (require Identity Bearer) or "dev" (bypass for unit tests).
	ProductAuth string
	// IdentityURL is the forge-identity base URL (host-reachable from workload containers).
	IdentityURL string
	// IdentityProjectID is the Identity project used for PAT issuance (optional; may be
	// loaded later from app_settings).
	IdentityProjectID string
	// JWTSigningKey signs optional app JWTs. Required — inject via Forge Secrets (51.04).
	JWTSigningKey string
}

func loadConfig() (config, error) {
	auth := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_PRODUCT_AUTH")))
	if auth == "" {
		auth = strings.ToLower(strings.TrimSpace(os.Getenv("PRODUCT_AUTH")))
	}
	if auth == "" {
		auth = "enforce"
	}
	if auth != "enforce" && auth != "dev" {
		auth = "enforce"
	}

	identityURL := strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL"))
	if identityURL == "" {
		identityURL = strings.TrimSpace(os.Getenv("IDENTITY_URL"))
	}
	if identityURL == "" {
		identityURL = "http://host.docker.internal:4002"
	}

	projectID := strings.TrimSpace(os.Getenv("FORGE_IDENTITY_PROJECT_ID"))
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("IDENTITY_PROJECT_ID"))
	}

	jwtKey := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY"))
	if jwtKey == "" {
		return config{}, errors.New("JWT_SIGNING_KEY is required (inject via Forge Secrets; no plaintext default)")
	}

	return config{
		ProductAuth:       auth,
		IdentityURL:       strings.TrimRight(identityURL, "/"),
		IdentityProjectID: projectID,
		JWTSigningKey:     jwtKey,
	}, nil
}
