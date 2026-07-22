// Package auth stores and resolves Forge CLI credentials per profile.
package auth

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	keyringService     = "forge-cli"
	defaultIdentityURL = "http://127.0.0.1:4002"
	backendKeychain    = "keychain"
	backendFile        = "file"
)

// Credentials holds the persisted auth material for one profile.
type Credentials struct {
	IdentityURL string
	Token       string
	Backend     string
}

// Store persists tokens keyed by CLI profile name.
type Store struct {
	path    string
	backend string // keychain|file|auto
	mu      sync.Mutex
}

// Error is a credential/auth failure that should map to the CLI auth exit code.
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

// Path returns the XDG-compatible credentials file path.
func Path() (string, error) {
	if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
		return filepath.Join(configHome, "forge", "credentials"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "forge", "credentials"), nil
}

// OpenStore creates a credential store using FORGE_CREDENTIALS_BACKEND when set.
func OpenStore() (*Store, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_CREDENTIALS_BACKEND")))
	switch backend {
	case "", "auto":
		backend = "auto"
	case backendKeychain, backendFile:
	default:
		return nil, &Error{Message: fmt.Sprintf("invalid FORGE_CREDENTIALS_BACKEND %q: expected keychain, file, or auto", backend)}
	}
	return &Store{path: path, backend: backend}, nil
}

// OpenStoreAt opens a store at an explicit path (tests).
func OpenStoreAt(path, backend string) *Store {
	if backend == "" {
		backend = backendFile
	}
	return &Store{path: path, backend: backend}
}

// DefaultIdentityURL returns the Identity endpoint for login/whoami/logout.
func DefaultIdentityURL() string {
	if url := strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL")); url != "" {
		return url
	}
	return defaultIdentityURL
}

// ResolveToken returns FORGE_TOKEN when set, otherwise the stored profile token.
func ResolveToken(store *Store, profile string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("FORGE_TOKEN")); token != "" {
		return token, nil
	}
	if store == nil {
		return "", nil
	}
	creds, err := store.Get(profile)
	if err != nil {
		return "", err
	}
	return creds.Token, nil
}

// Get loads credentials for a profile. Missing profiles return empty credentials.
func (s *Store) Get(profile string) (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.loadLocked()
	if err != nil {
		return Credentials{}, err
	}
	entry, ok := file[profile]
	if !ok {
		return Credentials{}, nil
	}
	creds := Credentials{
		IdentityURL: entry.identityURL,
		Backend:     entry.backend,
	}
	if creds.IdentityURL == "" {
		creds.IdentityURL = DefaultIdentityURL()
	}

	switch s.effectiveBackend(entry) {
	case backendKeychain:
		token, err := keyring.Get(keyringService, profile)
		if err != nil {
			if err == keyring.ErrNotFound {
				return Credentials{IdentityURL: creds.IdentityURL, Backend: backendKeychain}, nil
			}
			return Credentials{}, fmt.Errorf("read keychain token for profile %q: %w", profile, err)
		}
		creds.Token = token
		creds.Backend = backendKeychain
	default:
		creds.Token = entry.token
		creds.Backend = backendFile
	}
	return creds, nil
}

// Put stores credentials for a profile. Token values are never logged by callers.
func (s *Store) Put(profile string, creds Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(profile) == "" {
		return &Error{Message: "profile name is required"}
	}
	if strings.TrimSpace(creds.Token) == "" {
		return &Error{Message: "token is required"}
	}
	if creds.IdentityURL == "" {
		creds.IdentityURL = DefaultIdentityURL()
	}

	file, err := s.loadLocked()
	if err != nil {
		return err
	}

	backend := s.chooseWriteBackend()
	entry := profileEntry{identityURL: creds.IdentityURL, backend: backend}
	switch backend {
	case backendKeychain:
		if err := keyring.Set(keyringService, profile, creds.Token); err != nil {
			if s.backend == backendKeychain {
				return fmt.Errorf("store token in keychain: %w", err)
			}
			// Auto mode falls back to file when keychain is unavailable.
			backend = backendFile
			entry.backend = backendFile
			entry.token = creds.Token
		}
	default:
		entry.token = creds.Token
	}

	file[profile] = entry
	return s.saveLocked(file)
}

// Delete removes local credentials for a profile (keychain + file entry).
func (s *Store) Delete(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	entry, ok := file[profile]
	if ok && s.effectiveBackend(entry) == backendKeychain {
		_ = keyring.Delete(keyringService, profile)
	} else {
		_ = keyring.Delete(keyringService, profile)
	}
	delete(file, profile)
	if len(file) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove credentials file: %w", err)
		}
		return nil
	}
	return s.saveLocked(file)
}

type profileEntry struct {
	identityURL string
	token       string
	backend     string
}

func (s *Store) chooseWriteBackend() string {
	switch s.backend {
	case backendFile:
		return backendFile
	case backendKeychain:
		return backendKeychain
	default:
		if keychainUsable() {
			return backendKeychain
		}
		return backendFile
	}
}

func (s *Store) effectiveBackend(entry profileEntry) string {
	if entry.backend == backendKeychain || entry.backend == backendFile {
		return entry.backend
	}
	if entry.token != "" {
		return backendFile
	}
	if s.backend == backendFile {
		return backendFile
	}
	return backendKeychain
}

func keychainUsable() bool {
	const probeAccount = "__forge_cli_probe__"
	if err := keyring.Set(keyringService, probeAccount, "probe"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, probeAccount)
	return true
}

func (s *Store) loadLocked() (map[string]profileEntry, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return map[string]profileEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read credentials %q: %w", s.path, err)
	}
	return parseCredentials(string(data))
}

func (s *Store) saveLocked(file map[string]profileEntry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	content := formatCredentials(file)
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".credentials-*")
	if err != nil {
		return fmt.Errorf("create credentials temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("set credentials permissions: %w", err)
	}
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replace credentials: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}

func parseCredentials(content string) (map[string]profileEntry, error) {
	result := map[string]profileEntry{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	var current string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			profile := strings.TrimPrefix(section, "profile:")
			profile = strings.TrimSpace(profile)
			if profile == "" {
				return nil, fmt.Errorf("invalid credentials section %q", section)
			}
			current = profile
			if _, ok := result[current]; !ok {
				result[current] = profileEntry{}
			}
			continue
		}
		if current == "" {
			return nil, fmt.Errorf("credentials key outside profile section: %s", line)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid credentials line %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		entry := result[current]
		switch key {
		case "identity_url":
			entry.identityURL = value
		case "token":
			entry.token = value
		case "backend":
			entry.backend = value
		default:
			return nil, fmt.Errorf("unknown credentials key %q", key)
		}
		result[current] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func formatCredentials(file map[string]profileEntry) string {
	names := make([]string, 0, len(file))
	for name := range file {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		entry := file[name]
		b.WriteString("[profile:")
		b.WriteString(name)
		b.WriteString("]\n")
		if entry.identityURL != "" {
			b.WriteString("identity_url = ")
			b.WriteString(entry.identityURL)
			b.WriteString("\n")
		}
		if entry.backend == backendKeychain {
			b.WriteString("backend = keychain\n")
		} else if entry.token != "" {
			b.WriteString("token = ")
			b.WriteString(entry.token)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
