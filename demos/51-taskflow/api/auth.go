package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const identityContextKey contextKey = "taskflowIdentity"

// User is a TaskFlow app principal (roles are product admin/member, not platform RBAC).
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type authIdentity struct {
	User      *User
	Token     string
	Principal string
}

type signupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role,omitempty"` // optional; only honored for seed/bootstrap paths
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
	PAT   string `json:"pat"`
	JWT   string `json:"jwt,omitempty"`
	User  *User  `json:"user"`
}

func (s *server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	password := req.Password
	display := strings.TrimSpace(req.DisplayName)
	if display == "" {
		display = email
	}
	if email == "" || len(password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password (min 8) required"})
		return
	}
	role := "member"
	if req.Role == "admin" || req.Role == "member" {
		// Allow explicit role only when product auth is bypassed (seed helpers / tests).
		if s.cfg.ProductAuth != "enforce" {
			role = req.Role
		}
	}

	if s.identity == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "identity unavailable"})
		return
	}

	userID, err := s.identity.Register(r.Context(), email, password, display)
	if err != nil {
		// Idempotent-ish: if already registered, try login path instead.
		if !strings.Contains(strings.ToLower(err.Error()), "409") &&
			!strings.Contains(strings.ToLower(err.Error()), "conflict") &&
			!strings.Contains(strings.ToLower(err.Error()), "already") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "register failed", "detail": err.Error()})
			return
		}
		userID = ""
	}

	session, err := s.identity.Login(r.Context(), email, password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	_ = session

	if userID == "" {
		info, ierr := s.identity.Introspect(r.Context(), session)
		if ierr != nil || !info.Active {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		userID = firstNonEmpty(info.UserID, info.PrincipalID)
	}

	existing, err := s.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user lookup failed"})
		return
	}
	if existing != nil {
		role = existing.Role
		userID = existing.ID
	} else {
		if err := s.store.UpsertUser(r.Context(), userID, email, "identity-managed", role); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist user failed"})
			return
		}
	}

	pat, jwtStr, user, err := s.issueAppTokens(r.Context(), userID, email, role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token issuance failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{Token: pat, PAT: pat, JWT: jwtStr, User: user})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password required"})
		return
	}
	if s.identity == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "identity unavailable"})
		return
	}

	session, err := s.identity.Login(r.Context(), email, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	info, err := s.identity.Introspect(r.Context(), session)
	if err != nil || !info.Active {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	userID := firstNonEmpty(info.UserID, info.PrincipalID)

	user, err := s.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user lookup failed"})
		return
	}
	if user == nil {
		// First login after Identity-only register: create as member.
		if err := s.store.UpsertUser(r.Context(), userID, email, "identity-managed", "member"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist user failed"})
			return
		}
		user = &User{ID: userID, Email: email, Role: "member"}
	}

	pat, jwtStr, outUser, err := s.issueAppTokens(r.Context(), user.ID, user.Email, user.Role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token issuance failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: pat, PAT: pat, JWT: jwtStr, User: outUser})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if id == nil || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	writeJSON(w, http.StatusOK, id.User)
}

func (s *server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if id == nil || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	if id.User.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "required_role": "admin"})
		return
	}
	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project id required"})
		return
	}
	err := s.store.DeleteProject(r.Context(), projectID)
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) issueAppTokens(ctx context.Context, userID, email, role string) (pat, jwtStr string, user *User, err error) {
	projectID, err := s.resolveIdentityProjectID(ctx)
	if err != nil {
		return "", "", nil, err
	}
	// Platform membership role for the PAT (not app admin/member).
	if err := s.identity.AddProjectMember(ctx, projectID, userID, "developer"); err != nil {
		return "", "", nil, err
	}
	pat, err = s.identity.CreatePAT(ctx, userID, projectID, "developer")
	if err != nil {
		return "", "", nil, err
	}
	user = &User{ID: userID, Email: email, Role: role}
	jwtStr, err = mintAppJWT(s.cfg.JWTSigningKey, user, pat, 24*time.Hour)
	if err != nil {
		// JWT is optional convenience; PAT is the Identity-backed credential.
		jwtStr = ""
		err = nil
	}
	return pat, jwtStr, user, nil
}

func (s *server) resolveIdentityProjectID(ctx context.Context) (string, error) {
	if s.cfg.IdentityProjectID != "" {
		return s.cfg.IdentityProjectID, nil
	}
	v, err := s.store.GetSetting(ctx, "identity_project_id")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return "", errors.New("identity_project_id not configured (run seed.sh / demo bootstrap)")
	}
	s.cfg.IdentityProjectID = v
	return v, nil
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.ProductAuth != "enforce" {
			ctx := context.WithValue(r.Context(), identityContextKey, &authIdentity{
				User:      &User{ID: "dev", Email: "dev@taskflow.local", Role: "admin"},
				Token:     "dev",
				Principal: "dev",
			})
			next(w, r.WithContext(ctx))
			return
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		token := strings.TrimSpace(raw[7:])
		if token == "" || s.identity == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}

		bearer := token
		if looksLikeJWT(token) {
			claims, err := parseAppJWT(s.cfg.JWTSigningKey, token)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
				return
			}
			if claims.PAT == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
				return
			}
			bearer = claims.PAT
		}

		info, err := s.identity.Introspect(r.Context(), bearer)
		if err != nil || !info.Active {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		principal := firstNonEmpty(info.UserID, info.PrincipalID)
		user, err := s.store.GetUserByID(r.Context(), principal)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user lookup failed"})
			return
		}
		if user == nil {
			// Fallback: JWT may carry email/role when local row lags.
			if looksLikeJWT(token) {
				if claims, perr := parseAppJWT(s.cfg.JWTSigningKey, token); perr == nil && claims.Email != "" {
					user = &User{ID: principal, Email: claims.Email, Role: firstNonEmpty(claims.Role, "member")}
				}
			}
		}
		if user == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey, &authIdentity{
			User:      user,
			Token:     bearer,
			Principal: principal,
		})
		next(w, r.WithContext(ctx))
	}
}

func identityFrom(ctx context.Context) *authIdentity {
	v, _ := ctx.Value(identityContextKey).(*authIdentity)
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Minimal HS256 JWT (header.payload.sig) for SPA convenience. Identity introspect
// still validates the embedded PAT — the JWT is not a substitute for Identity.
type appJWTClaims struct {
	Sub  string `json:"sub"`
	Email string `json:"email"`
	Role string `json:"role"`
	PAT  string `json:"pat"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

func mintAppJWT(secret string, user *User, pat string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := appJWTClaims{
		Sub:   user.ID,
		Email: user.Email,
		Role:  user.Role,
		PAT:   pat,
		Iat:   now.Unix(),
		Exp:   now.Add(ttl).Unix(),
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig, nil
}

func looksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && !strings.HasPrefix(token, "forge_pat_") && !strings.HasPrefix(token, "forge_sat_")
}

func parseAppJWT(secret, token string) (*appJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expected, got) {
		return nil, errors.New("invalid signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims appJWTClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	if claims.Exp > 0 && time.Now().UTC().Unix() > claims.Exp {
		return nil, errors.New("expired")
	}
	return &claims, nil
}
