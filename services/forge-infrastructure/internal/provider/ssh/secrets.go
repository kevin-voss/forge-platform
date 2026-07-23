package ssh

import (
	"context"
	"fmt"
	"sync"
)

// SecretResolver loads an SSH private key by secret name (sshKeySecretRef.name).
type SecretResolver interface {
	ResolveSSHKey(ctx context.Context, secretName string) ([]byte, error)
}

// MapSecrets is an in-memory SecretResolver for tests and local fixtures.
type MapSecrets struct {
	mu   sync.RWMutex
	Keys map[string][]byte
}

// ResolveSSHKey returns the PEM bytes for secretName.
func (m *MapSecrets) ResolveSSHKey(ctx context.Context, secretName string) ([]byte, error) {
	_ = ctx
	if m == nil {
		return nil, fmt.Errorf("no secret resolver configured")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	key, ok := m.Keys[secretName]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("ssh key secret %q not found", secretName)
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	return cp, nil
}

// Set stores a key (test helper).
func (m *MapSecrets) Set(name string, pem []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Keys == nil {
		m.Keys = map[string][]byte{}
	}
	m.Keys[name] = pem
}
