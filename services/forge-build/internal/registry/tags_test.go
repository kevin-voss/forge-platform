package registry_test

import (
	"strings"
	"testing"

	"forge.local/services/forge-build/internal/registry"
)

func TestComputeRefsDeterministic(t *testing.T) {
	refs, err := registry.ComputeRefs(registry.TagInput{
		Registry:   "localhost:5000",
		Pattern:    "{project}-{service}",
		Project:    "acme",
		Service:    "api",
		Commit:     "ab12cd3deadbeef",
		BuildID:    "11111111-1111-4111-8111-111111111111",
		PushLatest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refs.Versioned != "localhost:5000/acme-api:ab12cd3-11111111" {
		t.Fatalf("versioned = %q", refs.Versioned)
	}
	if refs.Latest != "localhost:5000/acme-api:latest" {
		t.Fatalf("latest = %q", refs.Latest)
	}
	if refs.Tag != "ab12cd3-11111111" || refs.Repository != "localhost:5000/acme-api" {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestComputeRefsOmitsEmptyProject(t *testing.T) {
	refs, err := registry.ComputeRefs(registry.TagInput{
		Service:    "api",
		Commit:     "abc1234deadbeef",
		BuildID:    "22222222-2222-4222-8222-222222222222",
		PushLatest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refs.Versioned != "localhost:5000/api:abc1234-22222222" {
		t.Fatalf("versioned = %q", refs.Versioned)
	}
	if refs.Latest != "localhost:5000/api:latest" {
		t.Fatalf("latest = %q", refs.Latest)
	}
}

func TestComputeRefsSanitizesNames(t *testing.T) {
	refs, err := registry.ComputeRefs(registry.TagInput{
		Project:    "Acme Corp!!",
		Service:    "API_Service",
		Commit:     "deadbeef",
		BuildID:    "abcd1234-0000-4000-8000-000000000001",
		PushLatest: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refs.Versioned != "localhost:5000/acme-corp-api_service:deadbee-abcd1234" {
		t.Fatalf("versioned = %q", refs.Versioned)
	}
	if refs.Latest != "" {
		t.Fatalf("expected no latest, got %q", refs.Latest)
	}
}

func TestComputeRefsRejectsInjection(t *testing.T) {
	_, err := registry.ComputeRefs(registry.TagInput{
		Registry: "http://evil.example",
		Service:  "api",
		Commit:   "abc1234",
		BuildID:  "11111111-1111-4111-8111-111111111111",
	})
	if err == nil || !strings.Contains(err.Error(), "without scheme") {
		t.Fatalf("err = %v", err)
	}
}

func TestSanitizeComponent(t *testing.T) {
	if got := registry.SanitizeComponent("  Foo/Bar@Baz  "); got != "foo-bar-baz" {
		t.Fatalf("got %q", got)
	}
	if got := registry.SanitizeComponent(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
}

func TestShortHelpers(t *testing.T) {
	if got := registry.ShortSHA("abcdefghij"); got != "abcdefg" {
		t.Fatalf("ShortSHA = %q", got)
	}
	if got := registry.ShortBuildID("11111111-1111-4111-8111-111111111111"); got != "11111111" {
		t.Fatalf("ShortBuildID = %q", got)
	}
}
