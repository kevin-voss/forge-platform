package aws

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// API is the AWS EC2 subset used by the provider (injectable for tests).
type API interface {
	DescribeRegions(ctx context.Context) ([]RegionInfo, error)
	DescribeInstanceTypes(ctx context.Context, region string) ([]InstanceTypeInfo, error)

	DescribeInstances(ctx context.Context, region string, tags map[string]string) ([]Instance, error)
	GetInstance(ctx context.Context, region, instanceID string) (*Instance, error)
	RunInstances(ctx context.Context, region string, req RunInstancesRequest) (*Instance, error)
	TerminateInstance(ctx context.Context, region, instanceID string) error
	RebootInstance(ctx context.Context, region, instanceID string) error

	CreateVPC(ctx context.Context, region string, req CreateVPCRequest) (*VPC, error)
	DeleteVPC(ctx context.Context, region, vpcID string) error
	DescribeVPCs(ctx context.Context, region string, tags map[string]string) ([]VPC, error)

	CreateVolume(ctx context.Context, region string, req CreateVolumeRequest) (*Volume, error)
	AttachVolume(ctx context.Context, region, volumeID, instanceID string) error
	DetachVolume(ctx context.Context, region, volumeID string) error
	ModifyVolume(ctx context.Context, region, volumeID string, sizeGiB int) error
	DeleteVolume(ctx context.Context, region, volumeID string) error
	DescribeVolumes(ctx context.Context, region string, tags map[string]string) ([]Volume, error)

	AllocateAddress(ctx context.Context, region string, req AllocateAddressRequest) (*ElasticIP, error)
	AssociateAddress(ctx context.Context, region, allocID, instanceID string) error
	DisassociateAddress(ctx context.Context, region, assocID string) error
	ReleaseAddress(ctx context.Context, region, allocID string) error
	DescribeAddresses(ctx context.Context, region string, tags map[string]string) ([]ElasticIP, error)

	GetPricing(ctx context.Context, region, instanceType string) (float64, error)
}

// --- wire types -------------------------------------------------------------

type RegionInfo struct {
	ID   string
	Name string
}

type InstanceTypeInfo struct {
	ID        string
	CPU       int
	MemoryMiB int
	DiskGiB   int
	GPU       int
	HourlyUSD float64
}

type Instance struct {
	ID           string
	Name         string
	State        string
	InstanceType string
	PrivateIP    string
	PublicIP     string
	Region       string
	AZ           string
	Tags         map[string]string
	Created      time.Time
	VpcID        string
	SubnetID     string
}

type VPC struct {
	ID     string
	CIDR   string
	Region string
	Tags   map[string]string
	Subnet string // primary subnet id
	SG     string // default forge SG id
}

type Volume struct {
	ID         string
	SizeGiB    int
	InstanceID string
	Region     string
	Tags       map[string]string
	Created    time.Time
}

type ElasticIP struct {
	AllocationID  string
	AssociationID string
	PublicIP      string
	InstanceID    string
	Region        string
	Tags          map[string]string
}

type RunInstancesRequest struct {
	Name         string
	InstanceType string
	AMI          string
	UserData     string
	ClientToken  string
	Tags         map[string]string
	SubnetID     string
	SGID         string
}

type CreateVPCRequest struct {
	CIDR       string
	SubnetCIDR string
	Name       string
	Tags       map[string]string
}

type CreateVolumeRequest struct {
	SizeGiB    int
	AZ         string
	Name       string
	Tags       map[string]string
	InstanceID string
}

type AllocateAddressRequest struct {
	Name       string
	Tags       map[string]string
	InstanceID string
}

// Credentials holds static AWS keys (never cached to disk by the provider).
type Credentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

// CredentialSource loads AWS credentials per call.
type CredentialSource interface {
	Credentials(ctx context.Context) (Credentials, error)
}

// StaticCredentials is a fixed credential set for tests / local fixtures.
type StaticCredentials Credentials

func (s StaticCredentials) Credentials(ctx context.Context) (Credentials, error) {
	_ = ctx
	if strings.TrimSpace(s.AccessKeyID) == "" || strings.TrimSpace(s.SecretAccessKey) == "" {
		return Credentials{}, fmt.Errorf("aws credentials are empty")
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
	raw = strings.TrimSpace(raw)
	var c Credentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Credentials{}, fmt.Errorf("aws credentials secret must be JSON with accessKeyId/secretAccessKey: %w", err)
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.SecretAccessKey) == "" {
		return Credentials{}, fmt.Errorf("aws credentials missing accessKeyId or secretAccessKey")
	}
	return c, nil
}

// HTTPClient talks to EC2 (+ optional pricing) with SigV4 and rate-limit awareness.
// Endpoint defaults to https://ec2.{region}.amazonaws.com; override for fixture replay.
type HTTPClient struct {
	EndpointTemplate string // e.g. https://ec2.%s.amazonaws.com
	PricingEndpoint  string
	HTTP             *http.Client
	Creds            CredentialSource
	Limiter          *Limiter
	Log              *slog.Logger
	MaxRetries       int
	DefaultRegion    string

	requestsTotal atomic.Int64
}

func NewHTTPClient(creds CredentialSource, lim *Limiter, log *slog.Logger, defaultRegion string) *HTTPClient {
	if lim == nil {
		lim = NewLimiter(5)
	}
	if log == nil {
		log = slog.Default()
	}
	if defaultRegion == "" {
		defaultRegion = "eu-central-1"
	}
	return &HTTPClient{
		EndpointTemplate: "https://ec2.%s.amazonaws.com",
		PricingEndpoint:  "https://api.pricing.us-east-1.amazonaws.com",
		HTTP:             &http.Client{Timeout: 60 * time.Second},
		Creds:            creds,
		Limiter:          lim,
		Log:              log,
		MaxRetries:       8,
		DefaultRegion:    defaultRegion,
	}
}

func (c *HTTPClient) endpoint(region string) string {
	if region == "" {
		region = c.DefaultRegion
	}
	if strings.Contains(c.EndpointTemplate, "%s") {
		return fmt.Sprintf(c.EndpointTemplate, region)
	}
	return c.EndpointTemplate
}

func (c *HTTPClient) doQuery(ctx context.Context, region, action string, params url.Values, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if err := c.Limiter.Acquire(ctx); err != nil {
			return err
		}
		err := c.doQueryOnce(ctx, region, action, params, out)
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

func (c *HTTPClient) doQueryOnce(ctx context.Context, region, action string, params url.Values, out any) error {
	creds, err := c.Creds.Credentials(ctx)
	if err != nil {
		return err
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2016-11-15")
	body := params.Encode()
	endpoint := c.endpoint(region)
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Host", u.Host)
	now := time.Now().UTC()
	if err := signAWSV4(req, []byte(body), creds, "ec2", region, now); err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &transientError{err: err}
	}
	defer resp.Body.Close()
	c.Limiter.ObserveHeaders(resp.Header)
	c.requestsTotal.Add(1)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	c.Log.Info("aws api call",
		"event", "infra.provider.aws.api",
		"operation", action,
		"region", region,
		"status", resp.StatusCode,
		"metric", "forge_infra_aws_api_requests_total",
	)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 503 {
		return &rateLimitedError{Header: resp.Header.Clone(), Body: string(raw)}
	}
	if resp.StatusCode >= 500 {
		return &transientError{err: fmt.Errorf("aws %s: %s", action, resp.Status)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return &notFoundError{path: action}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.Contains(string(raw), "InvalidInstanceID.NotFound") ||
			strings.Contains(string(raw), "InvalidVolume.NotFound") ||
			strings.Contains(string(raw), "InvalidAddress.NotFound") {
			return &notFoundError{path: action}
		}
		return fmt.Errorf("aws %s: %s: %s", action, resp.Status, truncate(string(raw), 300))
	}
	if out == nil {
		return nil
	}
	// Prefer JSON fixture responses (for recorded HTTP replay against a local shim).
	if len(raw) > 0 && raw[0] == '{' {
		return json.Unmarshal(raw, out)
	}
	// Real EC2 returns XML; for production we decode a minimal JSON envelope when
	// FORGE_INFRA_AWS_API_BASE points at a recording shim. Static catalog covers
	// Describe* for offline use via Fake; HTTP path stores raw acknowledgement.
	if m, ok := out.(*map[string]any); ok {
		*m = map[string]any{"raw": string(raw), "action": action}
		return nil
	}
	return nil
}

// --- API methods (JSON fixture / shim oriented; fake covers unit tests) ------

func (c *HTTPClient) DescribeRegions(ctx context.Context) ([]RegionInfo, error) {
	var out struct {
		Regions []RegionInfo `json:"regions"`
	}
	if err := c.doQuery(ctx, c.DefaultRegion, "DescribeRegions", nil, &out); err != nil {
		return nil, err
	}
	if len(out.Regions) == 0 {
		return defaultRegions(), nil
	}
	return out.Regions, nil
}

func (c *HTTPClient) DescribeInstanceTypes(ctx context.Context, region string) ([]InstanceTypeInfo, error) {
	_ = ctx
	_ = region
	return defaultInstanceTypes(), nil
}

func (c *HTTPClient) DescribeInstances(ctx context.Context, region string, tags map[string]string) ([]Instance, error) {
	params := url.Values{}
	i := 1
	for k, v := range tags {
		params.Set(fmt.Sprintf("Filter.%d.Name", i), "tag:"+k)
		params.Set(fmt.Sprintf("Filter.%d.Value.1", i), v)
		i++
	}
	var out struct {
		Instances []Instance `json:"instances"`
	}
	if err := c.doQuery(ctx, region, "DescribeInstances", params, &out); err != nil {
		return nil, err
	}
	return out.Instances, nil
}

func (c *HTTPClient) GetInstance(ctx context.Context, region, instanceID string) (*Instance, error) {
	params := url.Values{}
	params.Set("InstanceId.1", instanceID)
	var out struct {
		Instances []Instance `json:"instances"`
	}
	if err := c.doQuery(ctx, region, "DescribeInstances", params, &out); err != nil {
		return nil, err
	}
	if len(out.Instances) == 0 {
		return nil, &notFoundError{path: instanceID}
	}
	return &out.Instances[0], nil
}

func (c *HTTPClient) RunInstances(ctx context.Context, region string, req RunInstancesRequest) (*Instance, error) {
	params := url.Values{}
	params.Set("ImageId", req.AMI)
	params.Set("InstanceType", req.InstanceType)
	params.Set("MinCount", "1")
	params.Set("MaxCount", "1")
	if req.ClientToken != "" {
		params.Set("ClientToken", req.ClientToken)
	}
	if req.UserData != "" {
		params.Set("UserData", req.UserData)
	}
	if req.SubnetID != "" {
		params.Set("SubnetId", req.SubnetID)
	}
	if req.SGID != "" {
		params.Set("SecurityGroupId.1", req.SGID)
	}
	ti := 1
	for k, v := range req.Tags {
		params.Set(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", ti), k)
		params.Set(fmt.Sprintf("TagSpecification.1.Tag.%d.Value", ti), v)
		ti++
	}
	params.Set("TagSpecification.1.ResourceType", "instance")
	var out struct {
		Instances []Instance `json:"instances"`
		Instance  *Instance  `json:"instance"`
	}
	if err := c.doQuery(ctx, region, "RunInstances", params, &out); err != nil {
		return nil, err
	}
	if out.Instance != nil {
		return out.Instance, nil
	}
	if len(out.Instances) > 0 {
		return &out.Instances[0], nil
	}
	return nil, fmt.Errorf("aws RunInstances: empty response")
}

func (c *HTTPClient) TerminateInstance(ctx context.Context, region, instanceID string) error {
	params := url.Values{}
	params.Set("InstanceId.1", instanceID)
	return c.doQuery(ctx, region, "TerminateInstances", params, nil)
}

func (c *HTTPClient) RebootInstance(ctx context.Context, region, instanceID string) error {
	params := url.Values{}
	params.Set("InstanceId.1", instanceID)
	return c.doQuery(ctx, region, "RebootInstances", params, nil)
}

func (c *HTTPClient) CreateVPC(ctx context.Context, region string, req CreateVPCRequest) (*VPC, error) {
	params := url.Values{}
	params.Set("CidrBlock", req.CIDR)
	body, _ := json.Marshal(req)
	_ = body
	var out struct {
		VPC *VPC `json:"vpc"`
	}
	if err := c.doQuery(ctx, region, "CreateVpc", params, &out); err != nil {
		return nil, err
	}
	if out.VPC == nil {
		return nil, fmt.Errorf("aws CreateVpc: empty response")
	}
	return out.VPC, nil
}

func (c *HTTPClient) DeleteVPC(ctx context.Context, region, vpcID string) error {
	params := url.Values{}
	params.Set("VpcId", vpcID)
	return c.doQuery(ctx, region, "DeleteVpc", params, nil)
}

func (c *HTTPClient) DescribeVPCs(ctx context.Context, region string, tags map[string]string) ([]VPC, error) {
	params := url.Values{}
	i := 1
	for k, v := range tags {
		params.Set(fmt.Sprintf("Filter.%d.Name", i), "tag:"+k)
		params.Set(fmt.Sprintf("Filter.%d.Value.1", i), v)
		i++
	}
	var out struct {
		VPCs []VPC `json:"vpcs"`
	}
	if err := c.doQuery(ctx, region, "DescribeVpcs", params, &out); err != nil {
		return nil, err
	}
	return out.VPCs, nil
}

func (c *HTTPClient) CreateVolume(ctx context.Context, region string, req CreateVolumeRequest) (*Volume, error) {
	params := url.Values{}
	params.Set("Size", fmt.Sprintf("%d", req.SizeGiB))
	params.Set("AvailabilityZone", req.AZ)
	params.Set("VolumeType", "gp3")
	var out struct {
		Volume *Volume `json:"volume"`
	}
	if err := c.doQuery(ctx, region, "CreateVolume", params, &out); err != nil {
		return nil, err
	}
	if out.Volume == nil {
		return nil, fmt.Errorf("aws CreateVolume: empty response")
	}
	return out.Volume, nil
}

func (c *HTTPClient) AttachVolume(ctx context.Context, region, volumeID, instanceID string) error {
	params := url.Values{}
	params.Set("VolumeId", volumeID)
	params.Set("InstanceId", instanceID)
	params.Set("Device", "/dev/sdf")
	return c.doQuery(ctx, region, "AttachVolume", params, nil)
}

func (c *HTTPClient) DetachVolume(ctx context.Context, region, volumeID string) error {
	params := url.Values{}
	params.Set("VolumeId", volumeID)
	return c.doQuery(ctx, region, "DetachVolume", params, nil)
}

func (c *HTTPClient) ModifyVolume(ctx context.Context, region, volumeID string, sizeGiB int) error {
	params := url.Values{}
	params.Set("VolumeId", volumeID)
	params.Set("Size", fmt.Sprintf("%d", sizeGiB))
	return c.doQuery(ctx, region, "ModifyVolume", params, nil)
}

func (c *HTTPClient) DeleteVolume(ctx context.Context, region, volumeID string) error {
	params := url.Values{}
	params.Set("VolumeId", volumeID)
	return c.doQuery(ctx, region, "DeleteVolume", params, nil)
}

func (c *HTTPClient) DescribeVolumes(ctx context.Context, region string, tags map[string]string) ([]Volume, error) {
	params := url.Values{}
	i := 1
	for k, v := range tags {
		params.Set(fmt.Sprintf("Filter.%d.Name", i), "tag:"+k)
		params.Set(fmt.Sprintf("Filter.%d.Value.1", i), v)
		i++
	}
	var out struct {
		Volumes []Volume `json:"volumes"`
	}
	if err := c.doQuery(ctx, region, "DescribeVolumes", params, &out); err != nil {
		return nil, err
	}
	return out.Volumes, nil
}

func (c *HTTPClient) AllocateAddress(ctx context.Context, region string, req AllocateAddressRequest) (*ElasticIP, error) {
	params := url.Values{}
	params.Set("Domain", "vpc")
	var out struct {
		Address *ElasticIP `json:"address"`
	}
	if err := c.doQuery(ctx, region, "AllocateAddress", params, &out); err != nil {
		return nil, err
	}
	if out.Address == nil {
		return nil, fmt.Errorf("aws AllocateAddress: empty response")
	}
	return out.Address, nil
}

func (c *HTTPClient) AssociateAddress(ctx context.Context, region, allocID, instanceID string) error {
	params := url.Values{}
	params.Set("AllocationId", allocID)
	params.Set("InstanceId", instanceID)
	return c.doQuery(ctx, region, "AssociateAddress", params, nil)
}

func (c *HTTPClient) DisassociateAddress(ctx context.Context, region, assocID string) error {
	params := url.Values{}
	params.Set("AssociationId", assocID)
	return c.doQuery(ctx, region, "DisassociateAddress", params, nil)
}

func (c *HTTPClient) ReleaseAddress(ctx context.Context, region, allocID string) error {
	params := url.Values{}
	params.Set("AllocationId", allocID)
	return c.doQuery(ctx, region, "ReleaseAddress", params, nil)
}

func (c *HTTPClient) DescribeAddresses(ctx context.Context, region string, tags map[string]string) ([]ElasticIP, error) {
	params := url.Values{}
	i := 1
	for k, v := range tags {
		params.Set(fmt.Sprintf("Filter.%d.Name", i), "tag:"+k)
		params.Set(fmt.Sprintf("Filter.%d.Value.1", i), v)
		i++
	}
	var out struct {
		Addresses []ElasticIP `json:"addresses"`
	}
	if err := c.doQuery(ctx, region, "DescribeAddresses", params, &out); err != nil {
		return nil, err
	}
	return out.Addresses, nil
}

func (c *HTTPClient) GetPricing(ctx context.Context, region, instanceType string) (float64, error) {
	_ = ctx
	for _, t := range defaultInstanceTypes() {
		if strings.EqualFold(t.ID, instanceType) {
			return t.HourlyUSD, nil
		}
	}
	_ = region
	return 0, fmt.Errorf("unknown machine type %q", instanceType)
}

func defaultRegions() []RegionInfo {
	return []RegionInfo{
		{ID: "eu-central-1", Name: "Europe (Frankfurt)"},
		{ID: "us-east-1", Name: "US East (N. Virginia)"},
		{ID: "us-west-2", Name: "US West (Oregon)"},
	}
}

func defaultInstanceTypes() []InstanceTypeInfo {
	return []InstanceTypeInfo{
		{ID: "t3.small", CPU: 2, MemoryMiB: 2048, DiskGiB: 20, HourlyUSD: 0.0208},
		{ID: "t3.medium", CPU: 2, MemoryMiB: 4096, DiskGiB: 20, HourlyUSD: 0.0416},
		{ID: "t3.large", CPU: 2, MemoryMiB: 8192, DiskGiB: 20, HourlyUSD: 0.0832},
		{ID: "m5.large", CPU: 2, MemoryMiB: 8192, DiskGiB: 20, HourlyUSD: 0.096},
		{ID: "m5.xlarge", CPU: 4, MemoryMiB: 16384, DiskGiB: 20, HourlyUSD: 0.192},
	}
}

// --- SigV4 ------------------------------------------------------------------

func signAWSV4(req *http.Request, payload []byte, creds Credentials, service, region string, t time.Time) error {
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		"", // query for POST body auth
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := awsSigningKey(creds.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", auth)
	return nil
}

func canonicalHeaders(req *http.Request) (string, string) {
	keys := []string{"host", "content-type", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("X-Amz-Security-Token") != "" {
		keys = append(keys, "x-amz-security-token")
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		v := req.Header.Get(k)
		if k == "host" {
			v = req.Host
			if v == "" {
				v = req.URL.Host
			}
		}
		if v == "" && k == "content-type" {
			v = req.Header.Get("Content-Type")
		}
		fmt.Fprintf(&b, "%s:%s\n", k, strings.TrimSpace(v))
	}
	return b.String(), strings.Join(keys, ";")
}

func awsSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(data)
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- errors -----------------------------------------------------------------

type rateLimitedError struct {
	Header http.Header
	Body   string
}

func (e *rateLimitedError) Error() string { return "aws rate limited (429)" }

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

func (e *notFoundError) Error() string { return "aws not found: " + e.path }

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
