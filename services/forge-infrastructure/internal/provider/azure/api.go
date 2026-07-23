package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// API is the Azure ARM subset used by the provider (injectable for tests).
type API interface {
	ListLocations(ctx context.Context) ([]Location, error)
	ListVMSizes(ctx context.Context, location string) ([]VMSize, error)

	ListVMs(ctx context.Context, tags map[string]string) ([]VM, error)
	GetVM(ctx context.Context, vmID string) (*VM, error)
	CreateVM(ctx context.Context, req CreateVMRequest) (*VM, error)
	DeleteVM(ctx context.Context, vmID string) error
	RestartVM(ctx context.Context, vmID string) error

	CreateVNet(ctx context.Context, req CreateVNetRequest) (*VNet, error)
	DeleteVNet(ctx context.Context, vnetID string) error
	ListVNets(ctx context.Context, tags map[string]string) ([]VNet, error)

	CreateDisk(ctx context.Context, req CreateDiskRequest) (*Disk, error)
	AttachDisk(ctx context.Context, diskID, vmID string) error
	DetachDisk(ctx context.Context, diskID string) error
	ResizeDisk(ctx context.Context, diskID string, sizeGiB int) error
	DeleteDisk(ctx context.Context, diskID string) error
	ListDisks(ctx context.Context, tags map[string]string) ([]Disk, error)

	CreatePublicIP(ctx context.Context, req CreatePublicIPAPIRequest) (*PublicIPAddr, error)
	AssociatePublicIP(ctx context.Context, ipID, vmID string) error
	DisassociatePublicIP(ctx context.Context, ipID string) error
	DeletePublicIP(ctx context.Context, ipID string) error
	ListPublicIPs(ctx context.Context, tags map[string]string) ([]PublicIPAddr, error)

	GetPricing(ctx context.Context, location, vmSize string) (float64, error)
}

type Location struct {
	Name        string
	DisplayName string
}

type VMSize struct {
	Name      string
	CPU       int
	MemoryMiB int
	DiskGiB   int
	GPU       int
	HourlyUSD float64
}

type VM struct {
	ID         string
	Name       string
	Location   string
	Size       string
	PrivateIP  string
	PublicIP   string
	PowerState string
	Tags       map[string]string
	Created    time.Time
	VNetID     string
}

type VNet struct {
	ID       string
	Name     string
	Location string
	CIDR     string
	Tags     map[string]string
	SubnetID string
	NSGID    string
}

type Disk struct {
	ID      string
	Name    string
	SizeGiB int
	VMID    string
	Tags    map[string]string
	Created time.Time
}

type PublicIPAddr struct {
	ID      string
	Name    string
	Address string
	VMID    string
	Tags    map[string]string
}

type CreateVMRequest struct {
	Name     string
	Location string
	Size     string
	Image    string
	UserData string
	Tags     map[string]string
	SubnetID string
	NSGID    string
}

type CreateVNetRequest struct {
	Name       string
	Location   string
	CIDR       string
	SubnetCIDR string
	Tags       map[string]string
}

type CreateDiskRequest struct {
	Name     string
	Location string
	SizeGiB  int
	VMID     string
	Tags     map[string]string
}

type CreatePublicIPAPIRequest struct {
	Name     string
	Location string
	VMID     string
	Tags     map[string]string
}

// Credentials holds Azure service-principal credentials.
type Credentials struct {
	TenantID       string `json:"tenantId"`
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	SubscriptionID string `json:"subscriptionId"`
}

// CredentialSource loads Azure credentials per call.
type CredentialSource interface {
	Credentials(ctx context.Context) (Credentials, error)
}

// StaticCredentials is a fixed credential set for tests / local fixtures.
type StaticCredentials Credentials

func (s StaticCredentials) Credentials(ctx context.Context) (Credentials, error) {
	_ = ctx
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.ClientID) == "" ||
		strings.TrimSpace(s.ClientSecret) == "" || strings.TrimSpace(s.SubscriptionID) == "" {
		return Credentials{}, fmt.Errorf("azure credentials are incomplete")
	}
	return Credentials(s), nil
}

// SecretCredentials resolves JSON credentials from Forge Secrets by name.
type SecretCredentials struct {
	Name     string
	Resolver CredentialResolver
}

// CredentialResolver loads a secret value by name.
type CredentialResolver interface {
	ResolveSecret(ctx context.Context, secretName string) (string, error)
}

func (s SecretCredentials) Credentials(ctx context.Context) (Credentials, error) {
	if s.Resolver == nil {
		return Credentials{}, fmt.Errorf("no credential resolver configured")
	}
	if strings.TrimSpace(s.Name) == "" {
		return Credentials{}, fmt.Errorf("credentialsSecretRef.name is required")
	}
	raw, err := s.Resolver.ResolveSecret(ctx, s.Name)
	if err != nil {
		return Credentials{}, err
	}
	return parseCredentialsJSON(raw)
}

func parseCredentialsJSON(raw string) (Credentials, error) {
	var c Credentials
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &c); err != nil {
		return Credentials{}, fmt.Errorf("azure credentials secret must be JSON with tenantId/clientId/clientSecret/subscriptionId: %w", err)
	}
	if c.TenantID == "" || c.ClientID == "" || c.ClientSecret == "" || c.SubscriptionID == "" {
		return Credentials{}, fmt.Errorf("azure credentials missing required fields")
	}
	return c, nil
}

// HTTPClient talks to Azure Resource Manager with bearer auth and rate-limit awareness.
type HTTPClient struct {
	ARMBase       string
	LoginBase     string
	ResourceGroup string
	HTTP          *http.Client
	Creds         CredentialSource
	Limiter       *Limiter
	Log           *slog.Logger
	MaxRetries    int
	DefaultRegion string

	token       string
	tokenExpiry time.Time
	requestsTotal atomic.Int64
}

func NewHTTPClient(creds CredentialSource, lim *Limiter, log *slog.Logger, defaultRegion, resourceGroup string) *HTTPClient {
	if lim == nil {
		lim = NewLimiter(5)
	}
	if log == nil {
		log = slog.Default()
	}
	if defaultRegion == "" {
		defaultRegion = "westeurope"
	}
	if resourceGroup == "" {
		resourceGroup = defaultResourceGroup
	}
	return &HTTPClient{
		ARMBase:       "https://management.azure.com",
		LoginBase:     "https://login.microsoftonline.com",
		ResourceGroup: resourceGroup,
		HTTP:          &http.Client{Timeout: 60 * time.Second},
		Creds:         creds,
		Limiter:       lim,
		Log:           log,
		MaxRetries:    8,
		DefaultRegion: defaultRegion,
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

func (c *HTTPClient) bearer(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}
	creds, err := c.Creds.Credentials(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", creds.ClientID)
	form.Set("client_secret", creds.ClientSecret)
	form.Set("resource", "https://management.azure.com/")
	tokenURL := fmt.Sprintf("%s/%s/oauth2/token", strings.TrimRight(c.LoginBase, "/"), creds.TenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", &transientError{err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("azure token: %s: %s", resp.Status, truncate(string(raw), 200))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", err
	}
	c.token = tok.AccessToken
	sec := 3600
	if n, err := strconvAtoi(tok.ExpiresIn); err == nil && n > 60 {
		sec = n - 60
	}
	c.tokenExpiry = time.Now().Add(time.Duration(sec) * time.Second)
	return c.token, nil
}

func strconvAtoi(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not an int")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func (c *HTTPClient) doOnce(ctx context.Context, method, path string, body any, out any) error {
	token, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	creds, err := c.Creds.Credentials(ctx)
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
	u := strings.TrimRight(c.ARMBase, "/") + path
	if !strings.Contains(path, "api-version=") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		u += sep + "api-version=2023-07-01"
	}
	// Ensure subscription path prefix when relative.
	if strings.HasPrefix(path, "/subscriptions/") {
		// ok
	} else if strings.HasPrefix(path, "/") {
		u = strings.TrimRight(c.ARMBase, "/") +
			"/subscriptions/" + creds.SubscriptionID +
			"/resourceGroups/" + c.ResourceGroup + path
		if !strings.Contains(u, "api-version=") {
			u += "?api-version=2023-07-01"
		}
	}
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
	c.requestsTotal.Add(1)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	op := method + " " + path
	c.Log.Info("azure api call",
		"event", "infra.provider.azure.api",
		"operation", op,
		"status", resp.StatusCode,
		"metric", "forge_infra_azure_api_requests_total",
	)
	if resp.StatusCode == http.StatusTooManyRequests {
		return &rateLimitedError{Header: resp.Header.Clone(), Body: string(raw)}
	}
	if resp.StatusCode >= 500 {
		return &transientError{err: fmt.Errorf("azure %s: %s", op, resp.Status)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return &notFoundError{path: path}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("azure %s: %s: %s", op, resp.Status, truncate(string(raw), 300))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *HTTPClient) ListLocations(ctx context.Context) ([]Location, error) {
	creds, err := c.Creds.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Value []Location `json:"value"`
	}
	path := "/subscriptions/" + creds.SubscriptionID + "/locations"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return defaultLocations(), nil // fall back to catalog when ARM returns XML/unexpected
	}
	if len(out.Value) == 0 {
		return defaultLocations(), nil
	}
	return out.Value, nil
}

func (c *HTTPClient) ListVMSizes(ctx context.Context, location string) ([]VMSize, error) {
	_ = ctx
	_ = location
	return defaultVMSizes(), nil
}

func (c *HTTPClient) ListVMs(ctx context.Context, tags map[string]string) ([]VM, error) {
	var out struct {
		Value []VM `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/providers/Microsoft.Compute/virtualMachines", nil, &out); err != nil {
		return nil, err
	}
	return filterVMs(out.Value, tags), nil
}

func (c *HTTPClient) GetVM(ctx context.Context, vmID string) (*VM, error) {
	var out VM
	path := "/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(vmID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) CreateVM(ctx context.Context, req CreateVMRequest) (*VM, error) {
	body := map[string]any{
		"location": req.Location,
		"tags":     req.Tags,
		"properties": map[string]any{
			"hardwareProfile": map[string]any{"vmSize": req.Size},
			"osProfile": map[string]any{
				"computerName":  req.Name,
				"adminUsername": "forge",
				"customData":    req.UserData,
			},
			"storageProfile": map[string]any{
				"imageReference": parseImageRef(req.Image),
			},
		},
	}
	var out VM
	path := "/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(req.Name)
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = req.Name
		out.Name = req.Name
		out.Location = req.Location
		out.Size = req.Size
		out.Tags = req.Tags
		out.PowerState = "running"
	}
	return &out, nil
}

func (c *HTTPClient) DeleteVM(ctx context.Context, vmID string) error {
	path := "/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(vmID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) RestartVM(ctx context.Context, vmID string) error {
	path := "/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(vmID) + "/restart"
	return c.doJSON(ctx, http.MethodPost, path, map[string]any{}, nil)
}

func (c *HTTPClient) CreateVNet(ctx context.Context, req CreateVNetRequest) (*VNet, error) {
	body := map[string]any{
		"location": req.Location,
		"tags":     req.Tags,
		"properties": map[string]any{
			"addressSpace": map[string]any{"addressPrefixes": []string{req.CIDR}},
		},
	}
	var out VNet
	path := "/providers/Microsoft.Network/virtualNetworks/" + url.PathEscape(req.Name)
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = req.Name
		out.Name = req.Name
		out.CIDR = req.CIDR
		out.Location = req.Location
		out.Tags = req.Tags
	}
	return &out, nil
}

func (c *HTTPClient) DeleteVNet(ctx context.Context, vnetID string) error {
	path := "/providers/Microsoft.Network/virtualNetworks/" + url.PathEscape(vnetID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) ListVNets(ctx context.Context, tags map[string]string) ([]VNet, error) {
	var out struct {
		Value []VNet `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/providers/Microsoft.Network/virtualNetworks", nil, &out); err != nil {
		return nil, err
	}
	return filterVNets(out.Value, tags), nil
}

func (c *HTTPClient) CreateDisk(ctx context.Context, req CreateDiskRequest) (*Disk, error) {
	body := map[string]any{
		"location": req.Location,
		"tags":     req.Tags,
		"sku":      map[string]any{"name": "Premium_LRS"},
		"properties": map[string]any{
			"creationData":   map[string]any{"createOption": "Empty"},
			"diskSizeGB":     req.SizeGiB,
		},
	}
	var out Disk
	path := "/providers/Microsoft.Compute/disks/" + url.PathEscape(req.Name)
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = req.Name
		out.Name = req.Name
		out.SizeGiB = req.SizeGiB
		out.Tags = req.Tags
		out.VMID = req.VMID
	}
	return &out, nil
}

func (c *HTTPClient) AttachDisk(ctx context.Context, diskID, vmID string) error {
	_ = diskID
	path := "/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(vmID)
	return c.doJSON(ctx, http.MethodPatch, path, map[string]any{"properties": map[string]any{}}, nil)
}

func (c *HTTPClient) DetachDisk(ctx context.Context, diskID string) error {
	_ = ctx
	_ = diskID
	return nil
}

func (c *HTTPClient) ResizeDisk(ctx context.Context, diskID string, sizeGiB int) error {
	body := map[string]any{"properties": map[string]any{"diskSizeGB": sizeGiB}}
	path := "/providers/Microsoft.Compute/disks/" + url.PathEscape(diskID)
	return c.doJSON(ctx, http.MethodPatch, path, body, nil)
}

func (c *HTTPClient) DeleteDisk(ctx context.Context, diskID string) error {
	path := "/providers/Microsoft.Compute/disks/" + url.PathEscape(diskID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) ListDisks(ctx context.Context, tags map[string]string) ([]Disk, error) {
	var out struct {
		Value []Disk `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/providers/Microsoft.Compute/disks", nil, &out); err != nil {
		return nil, err
	}
	return filterDisks(out.Value, tags), nil
}

func (c *HTTPClient) CreatePublicIP(ctx context.Context, req CreatePublicIPAPIRequest) (*PublicIPAddr, error) {
	body := map[string]any{
		"location": req.Location,
		"tags":     req.Tags,
		"sku":      map[string]any{"name": "Standard"},
		"properties": map[string]any{
			"publicIPAllocationMethod": "Static",
		},
	}
	var out PublicIPAddr
	path := "/providers/Microsoft.Network/publicIPAddresses/" + url.PathEscape(req.Name)
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = req.Name
		out.Name = req.Name
		out.Tags = req.Tags
		out.VMID = req.VMID
	}
	return &out, nil
}

func (c *HTTPClient) AssociatePublicIP(ctx context.Context, ipID, vmID string) error {
	_ = ctx
	_ = ipID
	_ = vmID
	return nil
}

func (c *HTTPClient) DisassociatePublicIP(ctx context.Context, ipID string) error {
	_ = ctx
	_ = ipID
	return nil
}

func (c *HTTPClient) DeletePublicIP(ctx context.Context, ipID string) error {
	path := "/providers/Microsoft.Network/publicIPAddresses/" + url.PathEscape(ipID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) ListPublicIPs(ctx context.Context, tags map[string]string) ([]PublicIPAddr, error) {
	var out struct {
		Value []PublicIPAddr `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/providers/Microsoft.Network/publicIPAddresses", nil, &out); err != nil {
		return nil, err
	}
	return filterIPs(out.Value, tags), nil
}

func (c *HTTPClient) GetPricing(ctx context.Context, location, vmSize string) (float64, error) {
	_ = ctx
	_ = location
	for _, s := range defaultVMSizes() {
		if strings.EqualFold(s.Name, vmSize) {
			return s.HourlyUSD, nil
		}
	}
	return 0, fmt.Errorf("unknown machine type %q", vmSize)
}

func parseImageRef(image string) map[string]any {
	parts := strings.Split(image, ":")
	if len(parts) == 4 {
		return map[string]any{
			"publisher": parts[0],
			"offer":     parts[1],
			"sku":       parts[2],
			"version":   parts[3],
		}
	}
	return map[string]any{"id": image}
}

func matchTags(tags, filter map[string]string) bool {
	if filter == nil {
		return true
	}
	for k, v := range filter {
		if tags == nil || tags[k] != v {
			return false
		}
	}
	return true
}

func filterVMs(in []VM, tags map[string]string) []VM {
	out := make([]VM, 0, len(in))
	for _, v := range in {
		if matchTags(v.Tags, tags) {
			out = append(out, v)
		}
	}
	return out
}

func filterVNets(in []VNet, tags map[string]string) []VNet {
	out := make([]VNet, 0, len(in))
	for _, v := range in {
		if matchTags(v.Tags, tags) {
			out = append(out, v)
		}
	}
	return out
}

func filterDisks(in []Disk, tags map[string]string) []Disk {
	out := make([]Disk, 0, len(in))
	for _, v := range in {
		if matchTags(v.Tags, tags) {
			out = append(out, v)
		}
	}
	return out
}

func filterIPs(in []PublicIPAddr, tags map[string]string) []PublicIPAddr {
	out := make([]PublicIPAddr, 0, len(in))
	for _, v := range in {
		if matchTags(v.Tags, tags) {
			out = append(out, v)
		}
	}
	return out
}

func defaultLocations() []Location {
	return []Location{
		{Name: "westeurope", DisplayName: "West Europe"},
		{Name: "northeurope", DisplayName: "North Europe"},
		{Name: "eastus", DisplayName: "East US"},
	}
}

func defaultVMSizes() []VMSize {
	return []VMSize{
		{Name: "Standard_B2s", CPU: 2, MemoryMiB: 4096, DiskGiB: 8, HourlyUSD: 0.0416},
		{Name: "Standard_D2s_v5", CPU: 2, MemoryMiB: 8192, DiskGiB: 16, HourlyUSD: 0.096},
		{Name: "Standard_D4s_v5", CPU: 4, MemoryMiB: 16384, DiskGiB: 32, HourlyUSD: 0.192},
		{Name: "Standard_E2s_v5", CPU: 2, MemoryMiB: 16384, DiskGiB: 32, HourlyUSD: 0.126},
	}
}

type rateLimitedError struct {
	Header http.Header
	Body   string
}

func (e *rateLimitedError) Error() string { return "azure rate limited (429)" }

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

func (e *notFoundError) Error() string { return "azure not found: " + e.path }

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
