package identity

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MetadataIdentityKey is the JetStream consumer metadata key for bound principal.
const MetadataIdentityKey = "forge_identity"

// Sentinel auth / identity errors.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidIdent = errors.New("invalid identity")
)

// ConsumerIdentity is the principal bound to a durable consumer.
type ConsumerIdentity struct {
	// Principal is the Identity principal_id (service-account or user id).
	// Empty means unbound (allowed only when auth mode is dev).
	Principal string `json:"principal,omitempty"`
}

// NormalizePrincipal validates an optional identity principal string.
func NormalizePrincipal(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if len(p) > 128 {
		return "", fmt.Errorf("%w: principal too long", ErrInvalidIdent)
	}
	for _, r := range p {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == ':' {
			continue
		}
		return "", fmt.Errorf("%w: principal has invalid character", ErrInvalidIdent)
	}
	return p, nil
}

// BindPrincipal chooses the durable's identity from optional client claim and auth principal.
// When enforce is true, authPrincipal is required and wins.
func BindPrincipal(enforce bool, authPrincipal, claimed string) (string, error) {
	claimed, err := NormalizePrincipal(claimed)
	if err != nil {
		return "", err
	}
	authPrincipal = strings.TrimSpace(authPrincipal)
	if enforce {
		if authPrincipal == "" {
			return "", ErrUnauthorized
		}
		if claimed != "" && claimed != authPrincipal {
			return "", fmt.Errorf("%w: identity does not match token principal", ErrForbidden)
		}
		return authPrincipal, nil
	}
	if claimed != "" {
		return claimed, nil
	}
	return authPrincipal, nil
}

// AuthorizeConsumer checks that the caller may act as the durable's bound identity.
func AuthorizeConsumer(enforce bool, boundPrincipal, authPrincipal string) error {
	if !enforce {
		return nil
	}
	if strings.TrimSpace(authPrincipal) == "" {
		return ErrUnauthorized
	}
	boundPrincipal = strings.TrimSpace(boundPrincipal)
	if boundPrincipal == "" {
		// Consumer created before binding / unbound — require any valid token.
		return nil
	}
	if authPrincipal != boundPrincipal {
		return ErrForbidden
	}
	return nil
}

// MetadataFromPrincipal builds JetStream consumer metadata for persistence.
func MetadataFromPrincipal(principal string) map[string]string {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return nil
	}
	return map[string]string{MetadataIdentityKey: principal}
}

// PrincipalFromMetadata extracts the bound principal from JetStream metadata.
func PrincipalFromMetadata(md map[string]string) string {
	if md == nil {
		return ""
	}
	return strings.TrimSpace(md[MetadataIdentityKey])
}
