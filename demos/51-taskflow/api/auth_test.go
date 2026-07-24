package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeIdentity struct {
	active   map[string]introspectResult
	users    map[string]string // email -> userID
	password map[string]string
	members  map[string]bool
	pats     int
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{
		active:   map[string]introspectResult{},
		users:    map[string]string{},
		password: map[string]string{},
		members:  map[string]bool{},
	}
}

func (f *fakeIdentity) Introspect(_ context.Context, token string) (introspectResult, error) {
	if token == "expired-token" {
		return introspectResult{Active: false}, nil
	}
	if token == "invalid-token" {
		return introspectResult{Active: false}, nil
	}
	if info, ok := f.active[token]; ok {
		return info, nil
	}
	return introspectResult{Active: false}, nil
}

func (f *fakeIdentity) Register(_ context.Context, email, password, _ string) (string, error) {
	email = strings.ToLower(email)
	if _, ok := f.users[email]; ok {
		return "", errors.New("conflict: already registered")
	}
	id := "user-" + email
	f.users[email] = id
	f.password[email] = password
	return id, nil
}

func (f *fakeIdentity) Login(_ context.Context, email, password string) (string, error) {
	email = strings.ToLower(email)
	if f.password[email] != password {
		return "", errors.New("invalid credentials")
	}
	id := f.users[email]
	tok := "session-" + id
	f.active[tok] = introspectResult{Active: true, PrincipalType: "user", PrincipalID: id, UserID: id}
	return tok, nil
}

func (f *fakeIdentity) AddProjectMember(_ context.Context, projectID, userID, _ string) error {
	f.members[projectID+":"+userID] = true
	return nil
}

func (f *fakeIdentity) CreatePAT(_ context.Context, ownerID, projectID, role string) (string, error) {
	f.pats++
	tok := "forge_pat_" + ownerID
	f.active[tok] = introspectResult{
		Active:        true,
		PrincipalType: "user",
		PrincipalID:   ownerID,
		UserID:        ownerID,
		ProjectID:     projectID,
		Role:          role,
	}
	return tok, nil
}

func testServer(t *testing.T, authMode string, id IdentityClient) (*server, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	_ = store.SetSetting(context.Background(), "identity_project_id", "proj-test")
	cfg := config{
		ProductAuth:       authMode,
		IdentityURL:       "http://identity.test",
		IdentityProjectID: "proj-test",
		JWTSigningKey:     "test-secret",
	}
	return newServer(store, cfg, id), store
}

func TestIntrospectMiddlewareValidInvalidExpired(t *testing.T) {
	id := newFakeIdentity()
	srv, store := testServer(t, "enforce", id)
	_ = store.UpsertUser(context.Background(), "user-a", "a@example.com", "x", "member")
	pat := "forge_pat_user-a"
	id.active[pat] = introspectResult{Active: true, PrincipalID: "user-a", UserID: "user-a", Role: "developer"}

	handler := srv.routes()

	// valid
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token status=%d body=%s", rec.Code, rec.Body.String())
	}

	// invalid
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d, want 401", rec.Code)
	}

	// expired / inactive
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer expired-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token status=%d, want 401", rec.Code)
	}

	// missing bearer
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d, want 401", rec.Code)
	}
}

func TestRoleGateDeleteProject(t *testing.T) {
	id := newFakeIdentity()
	srv, store := testServer(t, "enforce", id)
	_ = store.UpsertUser(context.Background(), "admin-1", "admin@example.com", "x", "admin")
	_ = store.UpsertUser(context.Background(), "member-1", "member@example.com", "x", "member")

	adminPAT := "forge_pat_admin-1"
	memberPAT := "forge_pat_member-1"
	id.active[adminPAT] = introspectResult{Active: true, PrincipalID: "admin-1", UserID: "admin-1"}
	id.active[memberPAT] = introspectResult{Active: true, PrincipalID: "member-1", UserID: "member-1"}

	handler := srv.routes()

	// member denied
	req := httptest.NewRequest(http.MethodDelete, "/projects/project-shared", nil)
	req.Header.Set("Authorization", "Bearer "+memberPAT)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member delete status=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// admin allowed
	req = httptest.NewRequest(http.MethodDelete, "/projects/project-shared", nil)
	req.Header.Set("Authorization", "Bearer "+adminPAT)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete status=%d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignupLoginIssuesPAT(t *testing.T) {
	id := newFakeIdentity()
	srv, _ := testServer(t, "enforce", id)
	handler := srv.routes()

	body := bytes.NewBufferString(`{"email":"new@example.com","password":"password1","displayName":"New"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created authResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(created.Token, "forge_pat_") || created.User == nil || created.User.Role != "member" {
		t.Fatalf("unexpected signup response: %+v", created)
	}
	if id.pats < 1 {
		t.Fatal("expected PAT issuance")
	}

	loginBody := bytes.NewBufferString(`{"email":"new@example.com","password":"password1"}`)
	req = httptest.NewRequest(http.MethodPost, "/auth/login", loginBody)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppJWTRoundTrip(t *testing.T) {
	user := &User{ID: "u1", Email: "u@example.com", Role: "admin"}
	tok, err := mintAppJWT("secret", user, "forge_pat_u1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := parseAppJWT("secret", tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.PAT != "forge_pat_u1" || claims.Role != "admin" {
		t.Fatalf("claims=%+v", claims)
	}
	if _, err := parseAppJWT("wrong", tok); err == nil {
		t.Fatal("expected bad signature")
	}
}
