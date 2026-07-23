package bootstrap

import (
	"fmt"
	"strings"
)

const maskedToken = "***"

// Payload is the provider-agnostic bootstrap core rendered once per CreateNode.
type Payload struct {
	ControlURL     string
	BootstrapToken string
	NodePool       string
	RuntimeImage   string
}

// CoreYAML renders the forge_bootstrap document (token present; do not log).
func (p Payload) CoreYAML() string {
	return fmt.Sprintf(`forge_bootstrap:
  control_url: %s
  bootstrap_token: %q
  node_pool: %s
  runtime_image: %s
`, p.ControlURL, p.BootstrapToken, p.NodePool, p.RuntimeImage)
}

// EnvFile renders /etc/forge/runtime.env contents.
func (p Payload) EnvFile() string {
	return fmt.Sprintf("FORGE_CONTROL_URL=%s\nFORGE_BOOTSTRAP_TOKEN=%s\n", p.ControlURL, p.BootstrapToken)
}

// EnvMap returns env keys for providers that inject process env (e.g. docker).
func (p Payload) EnvMap() map[string]string {
	return map[string]string{
		"FORGE_CONTROL_URL":          p.ControlURL,
		"FORGE_NODE_BOOTSTRAP_TOKEN": p.BootstrapToken,
	}
}

// LogSafe returns a map suitable for structured logs with the token masked.
func (p Payload) LogSafe() map[string]string {
	return map[string]string{
		"control_url":     p.ControlURL,
		"bootstrap_token": MaskToken(p.BootstrapToken),
		"node_pool":       p.NodePool,
		"runtime_image":   p.RuntimeImage,
	}
}

// MaskToken redacts a bootstrap token for logs.
func MaskToken(token string) string {
	if strings.TrimSpace(token) == "" {
		return ""
	}
	return maskedToken
}

// ContainsRawToken reports whether haystack includes the plaintext token.
func ContainsRawToken(haystack, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return strings.Contains(haystack, token)
}
