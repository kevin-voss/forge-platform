package main

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresJWTSigningKey(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "")
	t.Setenv("FORGE_PRODUCT_AUTH", "dev")
	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error when JWT_SIGNING_KEY is absent")
	}
	if !strings.Contains(err.Error(), "JWT_SIGNING_KEY") {
		t.Fatalf("error = %v, want mention of JWT_SIGNING_KEY", err)
	}
}

func TestLoadConfigReadsInjectedEnv(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "injected-from-forge-secrets")
	t.Setenv("FORGE_PRODUCT_AUTH", "enforce")
	t.Setenv("FORGE_IDENTITY_URL", "http://identity.example:8080/")
	t.Setenv("FORGE_IDENTITY_PROJECT_ID", "proj-1")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.JWTSigningKey != "injected-from-forge-secrets" {
		t.Fatalf("JWTSigningKey = %q", cfg.JWTSigningKey)
	}
	if cfg.ProductAuth != "enforce" {
		t.Fatalf("ProductAuth = %q", cfg.ProductAuth)
	}
	if cfg.IdentityURL != "http://identity.example:8080" {
		t.Fatalf("IdentityURL = %q", cfg.IdentityURL)
	}
	if cfg.IdentityProjectID != "proj-1" {
		t.Fatalf("IdentityProjectID = %q", cfg.IdentityProjectID)
	}
}
