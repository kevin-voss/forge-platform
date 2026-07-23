package events

import (
	"fmt"
	"strings"
)

// FamilyForSubject returns the stream family for a concrete subject when it
// matches a known configured family (e.g. application.crashed → application).
func FamilyForSubject(subject string, families []string) (string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if strings.ContainsAny(subject, "*> ") {
		return "", fmt.Errorf("subject must be a concrete token path without wildcards")
	}
	parts := strings.Split(subject, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("subject must be family.token (got %q)", subject)
	}
	for _, p := range parts {
		if p == "" {
			return "", fmt.Errorf("subject contains empty token")
		}
		for _, r := range p {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return "", fmt.Errorf("subject token %q has invalid characters", p)
		}
	}
	family := parts[0]
	for _, f := range families {
		if f == family {
			return family, nil
		}
	}
	return "", fmt.Errorf("unknown subject family %q", family)
}
