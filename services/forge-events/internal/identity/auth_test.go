package identity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubIntro struct {
	byToken map[string]Principal
	err     error
}

func (s *stubIntro) Introspect(_ context.Context, token string) (Principal, error) {
	if s.err != nil {
		return Principal{}, s.err
	}
	p, ok := s.byToken[token]
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	return p, nil
}

func TestBindPrincipalEnforce(t *testing.T) {
	p, err := BindPrincipal(true, "sa-1", "")
	if err != nil || p != "sa-1" {
		t.Fatalf("got %q err %v", p, err)
	}
	if _, err := BindPrincipal(true, "", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if _, err := BindPrincipal(true, "sa-1", "sa-2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthorizeConsumerMismatch(t *testing.T) {
	if err := AuthorizeConsumer(true, "sa-1", "sa-2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if err := AuthorizeConsumer(true, "sa-1", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if err := AuthorizeConsumer(true, "sa-1", "sa-1"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if err := AuthorizeConsumer(false, "sa-1", ""); err != nil {
		t.Fatalf("dev err = %v", err)
	}
}

func TestGateAuthenticateEnforce(t *testing.T) {
	g := &Gate{
		Mode: AuthModeEnforce,
		Introspector: &stubIntro{byToken: map[string]Principal{
			"good": {ID: "sa-1", Type: "service_account"},
		}},
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := g.Authenticate(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing token err = %v", err)
	}
	r.Header.Set("Authorization", "Bearer bad")
	if _, err := g.Authenticate(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token err = %v", err)
	}
	r.Header.Set("Authorization", "Bearer good")
	p, err := g.Authenticate(r)
	if err != nil || p.ID != "sa-1" {
		t.Fatalf("got %#v err %v", p, err)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	md := MetadataFromPrincipal("sa-9")
	if PrincipalFromMetadata(md) != "sa-9" {
		t.Fatalf("md = %#v", md)
	}
	if MetadataFromPrincipal("") != nil {
		t.Fatal("expected nil metadata for empty principal")
	}
}
