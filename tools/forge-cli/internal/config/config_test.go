package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	file := File{
		CurrentProfile: "file-profile",
		Profiles: map[string]Profile{
			"file-profile": {Endpoint: "https://file.example"},
			"env-profile":  {Endpoint: "https://env-profile.example"},
		},
	}
	tests := []struct {
		name                      string
		flagEndpoint, flagProfile string
		envEndpoint, envProfile   string
		wantEndpoint, wantProfile string
	}{
		{"default file", "", "", "", "", "https://file.example", "file-profile"},
		{"environment profile", "", "", "", "env-profile", "https://env-profile.example", "env-profile"},
		{"environment endpoint", "", "", "https://env-endpoint.example", "", "https://env-endpoint.example", "file-profile"},
		{"flag profile", "", "env-profile", "", "", "https://env-profile.example", "env-profile"},
		{"flag endpoint", "https://flag.example", "", "https://env-endpoint.example", "env-profile", "https://flag.example", "env-profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(file, tt.flagEndpoint, tt.flagProfile, tt.envEndpoint, tt.envProfile)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.Endpoint != tt.wantEndpoint || got.Profile != tt.wantProfile {
				t.Fatalf("Resolve() = %#v, want endpoint=%q profile=%q", got, tt.wantEndpoint, tt.wantProfile)
			}
		})
	}
}

func TestSaveLoadRoundTripUsesSecureFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forge", "config.yaml")
	want := File{
		CurrentProfile: "local",
		Profiles:       map[string]Profile{"local": {Endpoint: "http://127.0.0.1:4001"}},
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config mode = %o, want 600", mode)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.CurrentProfile != want.CurrentProfile || got.Profiles["local"] != want.Profiles["local"] {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestValidateEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:4001", "https://control.example"} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Errorf("ValidateEndpoint(%q) error = %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"", "control.example", "ftp://control.example", "https://user:pass@control.example"} {
		if err := ValidateEndpoint(endpoint); err == nil {
			t.Errorf("ValidateEndpoint(%q) succeeded, want error", endpoint)
		}
	}
}
