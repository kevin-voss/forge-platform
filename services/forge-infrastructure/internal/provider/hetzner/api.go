package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// API is the Hetzner Cloud subset used by the provider (injectable for tests).
type API interface {
	ListServers(ctx context.Context, labelSelector string) ([]Server, error)
	GetServer(ctx context.Context, id int64) (*Server, error)
	CreateServer(ctx context.Context, req CreateServerRequest) (*Server, error)
	DeleteServer(ctx context.Context, id int64) error
	RebootServer(ctx context.Context, id int64) error

	ListNetworks(ctx context.Context, labelSelector string) ([]Network, error)
	CreateNetwork(ctx context.Context, req CreateNetworkAPIRequest) (*Network, error)
	DeleteNetwork(ctx context.Context, id int64) error

	ListVolumes(ctx context.Context, labelSelector string) ([]Volume, error)
	CreateVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error)
	AttachVolume(ctx context.Context, volumeID, serverID int64) error
	DetachVolume(ctx context.Context, volumeID int64) error
	ResizeVolume(ctx context.Context, volumeID int64, sizeGiB int) error
	DeleteVolume(ctx context.Context, id int64) error

	ListFloatingIPs(ctx context.Context, labelSelector string) ([]FloatingIP, error)
	CreateFloatingIP(ctx context.Context, req CreateFloatingIPRequest) (*FloatingIP, error)
	AssignFloatingIP(ctx context.Context, ipID, serverID int64) error
	UnassignFloatingIP(ctx context.Context, ipID int64) error
	DeleteFloatingIP(ctx context.Context, id int64) error

	ListLocations(ctx context.Context) ([]Location, error)
	ListServerTypes(ctx context.Context) ([]ServerType, error)
}

// --- wire types -------------------------------------------------------------

type Server struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Created    string            `json:"created"`
	ServerType *ServerType       `json:"server_type"`
	Datacenter *Datacenter       `json:"datacenter"`
	PublicNet  *PublicNet        `json:"public_net"`
	Labels     map[string]string `json:"labels"`
}

type PublicNet struct {
	IPv4 *IPv4 `json:"ipv4"`
}

type IPv4 struct {
	IP string `json:"ip"`
}

type Datacenter struct {
	Location *Location `json:"location"`
}

type Location struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	City string `json:"city"`
}

type ServerType struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Cores        int       `json:"cores"`
	Memory       float64   `json:"memory"` // GB
	Disk         int       `json:"disk"`
	Prices       []STPrice `json:"prices"`
	Architecture string    `json:"architecture"`
}

type STPrice struct {
	Location    string `json:"location"`
	PriceHourly struct {
		Gross string `json:"gross"`
		Net   string `json:"net"`
	} `json:"price_hourly"`
}

type Network struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	IPRange string            `json:"ip_range"`
	Labels  map[string]string `json:"labels"`
}

type Volume struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Size     int               `json:"size"`
	Server   *int64            `json:"server"`
	Labels   map[string]string `json:"labels"`
	Location *Location         `json:"location"`
}

type FloatingIP struct {
	ID           int64             `json:"id"`
	IP           string            `json:"ip"`
	Type         string            `json:"type"`
	Server       *int64            `json:"server"`
	Labels       map[string]string `json:"labels"`
	HomeLocation *Location         `json:"home_location"`
}

type CreateServerRequest struct {
	Name       string            `json:"name"`
	ServerType string            `json:"server_type"`
	Image      string            `json:"image"`
	Location   string            `json:"location"`
	UserData   string            `json:"user_data,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Networks   []int64           `json:"networks,omitempty"`
	PublicNet  *CreatePublicNet  `json:"public_net,omitempty"`
}

type CreatePublicNet struct {
	EnableIPv4 bool `json:"enable_ipv4"`
	EnableIPv6 bool `json:"enable_ipv6"`
}

type CreateNetworkAPIRequest struct {
	Name    string            `json:"name"`
	IPRange string            `json:"ip_range"`
	Labels  map[string]string `json:"labels,omitempty"`
	Subnets []NetworkSubnet   `json:"subnets,omitempty"`
}

type NetworkSubnet struct {
	Type        string `json:"type"`
	NetworkZone string `json:"network_zone"`
	IPRange     string `json:"ip_range"`
}

type CreateVolumeRequest struct {
	Name      string            `json:"name"`
	Size      int               `json:"size"`
	Location  string            `json:"location"`
	Labels    map[string]string `json:"labels,omitempty"`
	Server    *int64            `json:"server,omitempty"`
	Automount bool              `json:"automount,omitempty"`
	Format    string            `json:"format,omitempty"`
}

type CreateFloatingIPRequest struct {
	Type         string            `json:"type"`
	HomeLocation string            `json:"home_location,omitempty"`
	Name         string            `json:"name,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Server       *int64            `json:"server,omitempty"`
}

// TokenSource loads the Hetzner API token (never cached to disk).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a fixed token for tests / local fixtures.
type StaticToken string

func (s StaticToken) Token(ctx context.Context) (string, error) {
	_ = ctx
	if strings.TrimSpace(string(s)) == "" {
		return "", fmt.Errorf("hetzner api token is empty")
	}
	return string(s), nil
}

// SecretToken resolves the token from Forge Secrets by name on every call.
type SecretToken struct {
	Name     string
	Resolver TokenResolver
}

// TokenResolver loads a secret value by name.
type TokenResolver interface {
	ResolveToken(ctx context.Context, secretName string) (string, error)
}

func (s SecretToken) Token(ctx context.Context) (string, error) {
	if s.Resolver == nil {
		return "", fmt.Errorf("no token resolver configured")
	}
	if strings.TrimSpace(s.Name) == "" {
		return "", fmt.Errorf("credentialsSecretRef.name is required")
	}
	return s.Resolver.ResolveToken(ctx, s.Name)
}

// MapTokens is an in-memory TokenResolver for tests.
type MapTokens struct {
	Values map[string]string
}

func (m *MapTokens) ResolveToken(ctx context.Context, secretName string) (string, error) {
	_ = ctx
	if m == nil || m.Values == nil {
		return "", fmt.Errorf("token secret %q not found", secretName)
	}
	v, ok := m.Values[secretName]
	if !ok || strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("token secret %q not found", secretName)
	}
	return v, nil
}

// HTTPClient talks to api.hetzner.cloud/v1 with rate-limit awareness.
type HTTPClient struct {
	BaseURL    string
	HTTP       *http.Client
	Tokens     TokenSource
	Limiter    *Limiter
	Log        *slog.Logger
	MaxRetries int

	requestsTotal atomic.Int64
	lastRemaining atomic.Int64
}

func NewHTTPClient(baseURL string, tokens TokenSource, lim *Limiter, log *slog.Logger) *HTTPClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.hetzner.cloud/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if lim == nil {
		lim = NewLimiter(5)
	}
	if log == nil {
		log = slog.Default()
	}
	return &HTTPClient{
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
		Tokens:     tokens,
		Limiter:    lim,
		Log:        log,
		MaxRetries: 8,
	}
}

func (c *HTTPClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if err := c.Limiter.Acquire(ctx); err != nil {
			return err
		}
		err := c.doOnce(ctx, method, path, body, out)
		c.Limiter.Release()
		if err == nil {
			c.Limiter.ResetSuccess()
			return nil
		}
		lastErr = err
		var re *rateLimitedError
		if AsRateLimited(err, &re) {
			if backoffErr := c.Limiter.Backoff429(ctx, re.Header); backoffErr != nil {
				return backoffErr
			}
			continue
		}
		var te *transientError
		if AsTransient(err, &te) && attempt < c.MaxRetries {
			delay := time.Duration(200*(1<<attempt)) * time.Millisecond
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			if sleepErr := c.Limiter.sleep(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		return err
	}
	return lastErr
}

func (c *HTTPClient) doOnce(ctx context.Context, method, path string, body any, out any) error {
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &transientError{err: err}
	}
	defer resp.Body.Close()
	c.Limiter.ObserveHeaders(resp.Header)
	if rem := resp.Header.Get("RateLimit-Remaining"); rem != "" {
		if n, err := strconv.ParseInt(rem, 10, 64); err == nil {
			c.lastRemaining.Store(n)
		}
	}
	c.requestsTotal.Add(1)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	c.Log.Info("hetzner api call",
		"event", "infra.provider.hetzner.api",
		"method", method,
		"path", path,
		"status", resp.StatusCode,
		"rate_limit_remaining", resp.Header.Get("RateLimit-Remaining"),
		"metric", "forge_infra_hetzner_api_requests_total",
	)
	if resp.StatusCode == http.StatusTooManyRequests {
		return &rateLimitedError{Header: resp.Header.Clone(), Body: string(raw)}
	}
	if resp.StatusCode >= 500 {
		return &transientError{err: fmt.Errorf("hetzner %s %s: %s", method, path, resp.Status)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return &notFoundError{path: path}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hetzner %s %s: %s: %s", method, path, resp.Status, truncate(string(raw), 300))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode hetzner response: %w", err)
	}
	return nil
}

func (c *HTTPClient) ListServers(ctx context.Context, labelSelector string) ([]Server, error) {
	q := url.Values{}
	if labelSelector != "" {
		q.Set("label_selector", labelSelector)
	}
	path := "/servers"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var resp struct {
		Servers []Server `json:"servers"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Servers, nil
}

func (c *HTTPClient) GetServer(ctx context.Context, id int64) (*Server, error) {
	var resp struct {
		Server Server `json:"server"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/servers/"+strconv.FormatInt(id, 10), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Server, nil
}

func (c *HTTPClient) CreateServer(ctx context.Context, req CreateServerRequest) (*Server, error) {
	var resp struct {
		Server Server `json:"server"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/servers", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Server, nil
}

func (c *HTTPClient) DeleteServer(ctx context.Context, id int64) error {
	return c.doJSON(ctx, http.MethodDelete, "/servers/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *HTTPClient) RebootServer(ctx context.Context, id int64) error {
	return c.doJSON(ctx, http.MethodPost, "/servers/"+strconv.FormatInt(id, 10)+"/actions/reboot", map[string]any{}, nil)
}

func (c *HTTPClient) ListNetworks(ctx context.Context, labelSelector string) ([]Network, error) {
	path := "/networks"
	if labelSelector != "" {
		path += "?label_selector=" + url.QueryEscape(labelSelector)
	}
	var resp struct {
		Networks []Network `json:"networks"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Networks, nil
}

func (c *HTTPClient) CreateNetwork(ctx context.Context, req CreateNetworkAPIRequest) (*Network, error) {
	var resp struct {
		Network Network `json:"network"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/networks", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Network, nil
}

func (c *HTTPClient) DeleteNetwork(ctx context.Context, id int64) error {
	return c.doJSON(ctx, http.MethodDelete, "/networks/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *HTTPClient) ListVolumes(ctx context.Context, labelSelector string) ([]Volume, error) {
	path := "/volumes"
	if labelSelector != "" {
		path += "?label_selector=" + url.QueryEscape(labelSelector)
	}
	var resp struct {
		Volumes []Volume `json:"volumes"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

func (c *HTTPClient) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error) {
	var resp struct {
		Volume Volume `json:"volume"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/volumes", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Volume, nil
}

func (c *HTTPClient) AttachVolume(ctx context.Context, volumeID, serverID int64) error {
	body := map[string]any{"server": serverID, "automount": false}
	return c.doJSON(ctx, http.MethodPost, "/volumes/"+strconv.FormatInt(volumeID, 10)+"/actions/attach", body, nil)
}

func (c *HTTPClient) DetachVolume(ctx context.Context, volumeID int64) error {
	return c.doJSON(ctx, http.MethodPost, "/volumes/"+strconv.FormatInt(volumeID, 10)+"/actions/detach", map[string]any{}, nil)
}

func (c *HTTPClient) ResizeVolume(ctx context.Context, volumeID int64, sizeGiB int) error {
	return c.doJSON(ctx, http.MethodPost, "/volumes/"+strconv.FormatInt(volumeID, 10)+"/actions/resize", map[string]any{"size": sizeGiB}, nil)
}

func (c *HTTPClient) DeleteVolume(ctx context.Context, id int64) error {
	return c.doJSON(ctx, http.MethodDelete, "/volumes/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *HTTPClient) ListFloatingIPs(ctx context.Context, labelSelector string) ([]FloatingIP, error) {
	path := "/floating_ips"
	if labelSelector != "" {
		path += "?label_selector=" + url.QueryEscape(labelSelector)
	}
	var resp struct {
		FloatingIPs []FloatingIP `json:"floating_ips"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.FloatingIPs, nil
}

func (c *HTTPClient) CreateFloatingIP(ctx context.Context, req CreateFloatingIPRequest) (*FloatingIP, error) {
	var resp struct {
		FloatingIP FloatingIP `json:"floating_ip"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/floating_ips", req, &resp); err != nil {
		return nil, err
	}
	return &resp.FloatingIP, nil
}

func (c *HTTPClient) AssignFloatingIP(ctx context.Context, ipID, serverID int64) error {
	return c.doJSON(ctx, http.MethodPost, "/floating_ips/"+strconv.FormatInt(ipID, 10)+"/actions/assign", map[string]any{"server": serverID}, nil)
}

func (c *HTTPClient) UnassignFloatingIP(ctx context.Context, ipID int64) error {
	return c.doJSON(ctx, http.MethodPost, "/floating_ips/"+strconv.FormatInt(ipID, 10)+"/actions/unassign", map[string]any{}, nil)
}

func (c *HTTPClient) DeleteFloatingIP(ctx context.Context, id int64) error {
	return c.doJSON(ctx, http.MethodDelete, "/floating_ips/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *HTTPClient) ListLocations(ctx context.Context) ([]Location, error) {
	var resp struct {
		Locations []Location `json:"locations"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/locations", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Locations, nil
}

func (c *HTTPClient) ListServerTypes(ctx context.Context) ([]ServerType, error) {
	var resp struct {
		ServerTypes []ServerType `json:"server_types"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/server_types", nil, &resp); err != nil {
		return nil, err
	}
	return resp.ServerTypes, nil
}

// --- errors -----------------------------------------------------------------

type rateLimitedError struct {
	Header http.Header
	Body   string
}

func (e *rateLimitedError) Error() string { return "hetzner rate limited (429)" }

func AsRateLimited(err error, target **rateLimitedError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*rateLimitedError); ok {
		*target = e
		return true
	}
	return false
}

type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func AsTransient(err error, target **transientError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*transientError); ok {
		*target = e
		return true
	}
	return false
}

type notFoundError struct{ path string }

func (e *notFoundError) Error() string { return "hetzner not found: " + e.path }

func IsNotFound(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
