package idempotency

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxIdempotencyKeyLen is the JetStream msg-id length limit.
const MaxIdempotencyKeyLen = 64

// NormalizeIdempotencyKey trims and validates a publish idempotency key.
// Empty input is allowed (caller then uses a generated event id).
func NormalizeIdempotencyKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", nil
	}
	if len(key) > MaxIdempotencyKeyLen {
		return "", fmt.Errorf("idempotency key exceeds %d bytes", MaxIdempotencyKeyLen)
	}
	for _, r := range key {
		if unicode.IsPrint(r) && r != ' ' {
			continue
		}
		if r == ' ' || r == '\t' {
			return "", fmt.Errorf("idempotency key must not contain whitespace")
		}
		return "", fmt.Errorf("idempotency key contains invalid character")
	}
	return key, nil
}
