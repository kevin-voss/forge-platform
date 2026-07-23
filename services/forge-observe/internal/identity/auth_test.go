package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubClient struct {
	p     Principal
	allow bool
}

func (s *stubClient) Introspect(_ context.Context, token string) (Principal, error) {
	if token != "good" {
		return Principal{}, ErrUnauthorized
	}
	return s.p, nil
}

func (s *stubClient) Check(_ context.Context, _ Principal, _ string, _ string) (bool, error) {
	return s.allow, nil
}

func TestParseAuthMode(t *testing.T) {
	m, err := ParseAuthMode("")
	if err != nil || m != AuthModeDev {
		t.Fatalf("got %q %v", m, err)
	}
	if _, err := ParseAuthMode("weird"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGateEnforceUnauthorized(t *testing.T) {
	g := &Gate{Mode: AuthModeEnforce, Client: &stubClient{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/logs", nil)
	if _, err := g.Authenticate(req); err != ErrUnauthorized {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthorizeProjectForbidden(t *testing.T) {
	g := &Gate{
		Mode: AuthModeEnforce,
		Client: &stubClient{
			p: Principal{
				ID: "u1", Type: "user",
				AllowedProjs: map[string]struct{}{"prj_ok": {}},
			},
			allow: false,
		},
	}
	p := Principal{ID: "u1", Type: "user", AllowedProjs: map[string]struct{}{"prj_ok": {}}}
	if _, err := g.AuthorizeProject(context.Background(), p, "prj_other"); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}
