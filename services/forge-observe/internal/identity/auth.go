package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AuthMode controls whether log queries require Identity tokens + project authz.
type AuthMode string

const (
	AuthModeDev     AuthMode = "dev"
	AuthModeEnforce AuthMode = "enforce"
)

// Sentinel errors.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

// ParseAuthMode validates FORGE_AUTH_MODE (default dev).
func ParseAuthMode(raw string) (AuthMode, error) {
	m := AuthMode(strings.ToLower(strings.TrimSpace(raw)))
	if m == "" {
		return AuthModeDev, nil
	}
	switch m {
	case AuthModeDev, AuthModeEnforce:
		return m, nil
	default:
		return "", fmt.Errorf("FORGE_AUTH_MODE must be enforce|dev, got %q", raw)
	}
}

// Principal is the authenticated caller.
type Principal struct {
	ID           string
	Type         string
	ProjectID    string
	AllowedProjs map[string]struct{}
}

// MembershipProject is one project membership from introspect.
type MembershipProject struct {
	ProjectID string `json:"project_id"`
}

type memberships struct {
	Projects []MembershipProject `json:"projects"`
}

type introspectRequest struct {
	Token string `json:"token"`
}

type introspectResponse struct {
	Active        bool         `json:"active"`
	PrincipalType string       `json:"principal_type"`
	PrincipalID   string       `json:"principal_id"`
	ProjectID     string       `json:"project_id"`
	Memberships   *memberships `json:"memberships"`
}

type authzRequest struct {
	Principal struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"principal"`
	ProjectID string `json:"project_id"`
	Action    string `json:"action"`
}

type authzResponse struct {
	Allow  bool   `json:"allow"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// Client talks to Identity introspect + authz/check.
type Client interface {
	Introspect(ctx context.Context, token string) (Principal, error)
	Check(ctx context.Context, p Principal, projectID, action string) (bool, error)
}

// HTTPClient is a caching Identity HTTP client.
type HTTPClient struct {
	baseURL  string
	client   *http.Client
	cacheTTL time.Duration
	mu       sync.Mutex
	cache    map[string]cachedPrincipal
	log      *slog.Logger
}

type cachedPrincipal struct {
	p         Principal
	expiresAt time.Time
}

// NewHTTPClient builds an Identity client.
func NewHTTPClient(baseURL string, cacheTTLS int, log *slog.Logger) *HTTPClient {
	if log == nil {
		log = slog.Default()
	}
	ttl := time.Duration(cacheTTLS) * time.Second
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &HTTPClient{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:   &http.Client{Timeout: 5 * time.Second},
		cacheTTL: ttl,
		cache:    make(map[string]cachedPrincipal),
		log:      log,
	}
}

// Introspect validates a bearer token.
func (h *HTTPClient) Introspect(ctx context.Context, token string) (Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	if p, ok := h.getCached(token); ok {
		return p, nil
	}
	body, _ := json.Marshal(introspectRequest{Token: token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/auth/introspect", bytes.NewReader(body))
	if err != nil {
		return Principal{}, fmt.Errorf("introspect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return Principal{}, fmt.Errorf("introspect transport: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Principal{}, fmt.Errorf("introspect status %d", resp.StatusCode)
	}
	var out introspectResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Principal{}, fmt.Errorf("introspect decode: %w", err)
	}
	if !out.Active || strings.TrimSpace(out.PrincipalID) == "" {
		return Principal{}, ErrUnauthorized
	}
	p := Principal{
		ID:           out.PrincipalID,
		Type:         out.PrincipalType,
		ProjectID:    strings.TrimSpace(out.ProjectID),
		AllowedProjs: map[string]struct{}{},
	}
	if p.ProjectID != "" {
		p.AllowedProjs[p.ProjectID] = struct{}{}
	}
	if out.Memberships != nil {
		for _, m := range out.Memberships.Projects {
			id := strings.TrimSpace(m.ProjectID)
			if id != "" {
				p.AllowedProjs[id] = struct{}{}
			}
		}
	}
	h.putCached(token, p)
	return p, nil
}

// Check calls Identity POST /v1/authz/check.
func (h *HTTPClient) Check(ctx context.Context, p Principal, projectID, action string) (bool, error) {
	var body authzRequest
	body.Principal.Type = p.Type
	if body.Principal.Type == "" {
		body.Principal.Type = "user"
	}
	body.Principal.ID = p.ID
	body.ProjectID = projectID
	body.Action = action
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/authz/check", bytes.NewReader(raw))
	if err != nil {
		return false, fmt.Errorf("authz request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("authz transport: %w", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("authz status %d", resp.StatusCode)
	}
	var out authzResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return false, fmt.Errorf("authz decode: %w", err)
	}
	return out.Allow, nil
}

func (h *HTTPClient) getCached(token string) (Principal, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.cache[token]
	if !ok || time.Now().After(c.expiresAt) {
		return Principal{}, false
	}
	return c.p, true
}

func (h *HTTPClient) putCached(token string, p Principal) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[token] = cachedPrincipal{p: p, expiresAt: time.Now().Add(h.cacheTTL)}
}

// Gate enforces optional auth on Observe log queries.
type Gate struct {
	Mode   AuthMode
	Client Client
	Log    *slog.Logger
	Action string // default project.read
}

// Enforce reports whether tokens are required.
func (g *Gate) Enforce() bool {
	return g != nil && g.Mode == AuthModeEnforce
}

// Authenticate extracts and validates a bearer token when enforce is on.
func (g *Gate) Authenticate(r *http.Request) (Principal, error) {
	token := bearerToken(r)
	if g == nil || !g.Enforce() {
		return Principal{}, nil
	}
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	if g.Client == nil {
		return Principal{}, fmt.Errorf("identity client not configured")
	}
	p, err := g.Client.Introspect(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return Principal{}, ErrUnauthorized
		}
		if g.Log != nil {
			g.Log.Warn("identity introspect failed", "error", err.Error())
		}
		return Principal{}, ErrUnauthorized
	}
	return p, nil
}

// AuthorizeProject ensures the principal may read logs for projectID.
// When projectID is empty, returns allowed project set for post-filtering.
func (g *Gate) AuthorizeProject(ctx context.Context, p Principal, projectID string) (map[string]struct{}, error) {
	if g == nil || !g.Enforce() {
		return nil, nil
	}
	action := g.Action
	if action == "" {
		action = "project.read"
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		// Fast path: scoped token / known memberships.
		if len(p.AllowedProjs) > 0 {
			if _, ok := p.AllowedProjs[projectID]; !ok {
				return nil, ErrForbidden
			}
		}
		if g.Client != nil {
			allow, err := g.Client.Check(ctx, p, projectID, action)
			if err != nil {
				if g.Log != nil {
					g.Log.Warn("authz check failed", "error", err.Error(), "project", projectID)
				}
				return nil, ErrForbidden
			}
			if !allow {
				return nil, ErrForbidden
			}
		}
		return map[string]struct{}{projectID: {}}, nil
	}
	// No project filter: restrict to memberships when known.
	if len(p.AllowedProjs) == 0 {
		return nil, fmt.Errorf("%w: project filter required when memberships unknown", ErrForbidden)
	}
	return p.AllowedProjs, nil
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
