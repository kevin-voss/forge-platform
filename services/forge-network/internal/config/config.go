package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-network.
type Config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	ShutdownGrace  time.Duration

	DatabaseURL            string
	DatabaseSchema         string
	DatabasePoolMax        int
	DatabaseMigrateOnStart bool

	ClusterCIDR          string
	NodePrefixLength     int
	IPv6Enabled          bool
	LeaseReclaimInterval time.Duration
	ProviderCIDRs        []string
	DockerHost           string
	DockerCollisionCheck bool

	// WireGuard peer control plane (22.03).
	WgMTU            int
	WgKeepaliveS     int
	WgTopology       string // mesh|hub (hub documented, not implemented)
	WgBackend        string // kernel|userspace|auto — Runtime consumes; documented here
	WgRotationWindow time.Duration

	// Transport mode default when membership/colocation do not select a mode (22.04).
	ModeDefault string // docker|provider-private|wireguard

	// NetworkPolicy cluster fallback (22.05).
	PolicyDefault string // allow-within-environment|deny-all
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		portRaw = "8080"
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("PORT must be an integer 1–65535, got %q", portRaw)
	}

	level := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_LOG_LEVEL")))
	if level == "" {
		level = "info"
	}
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("FORGE_LOG_LEVEL must be debug|info|warn|error, got %q", level)
	}

	name := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if name == "" {
		name = "forge-network"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}

	graceRaw := strings.TrimSpace(os.Getenv("FORGE_SHUTDOWN_GRACE_SECONDS"))
	if graceRaw == "" {
		graceRaw = "10"
	}
	graceSecs, err := strconv.Atoi(graceRaw)
	if err != nil || graceSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got %q", graceRaw)
	}

	dbURL := strings.TrimSpace(os.Getenv("FORGE_DATABASE_URL"))
	if dbURL == "" {
		// Prefer DSN; also accept discrete FORGE_DB_* knobs from the step doc.
		dbURL = buildDatabaseURLFromParts()
	}
	if dbURL == "" {
		dbURL = "postgres://forge:forge@localhost:5432/forge?sslmode=disable"
	}
	schema := strings.TrimSpace(os.Getenv("FORGE_DATABASE_SCHEMA"))
	if schema == "" {
		schema = strings.TrimSpace(os.Getenv("FORGE_DB_SCHEMA"))
	}
	if schema == "" {
		schema = "network"
	}
	poolRaw := strings.TrimSpace(os.Getenv("FORGE_DATABASE_POOL_MAX"))
	if poolRaw == "" {
		poolRaw = "10"
	}
	poolMax, err := strconv.Atoi(poolRaw)
	if err != nil || poolMax < 1 {
		return Config{}, fmt.Errorf("FORGE_DATABASE_POOL_MAX must be a positive integer, got %q", poolRaw)
	}
	migrateOnStart := true
	migrateRaw := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_DATABASE_MIGRATE_ON_START")))
	switch migrateRaw {
	case "false", "0", "no":
		migrateOnStart = false
	}

	clusterCIDR := strings.TrimSpace(os.Getenv("FORGE_NETWORK_CLUSTER_CIDR"))
	if clusterCIDR == "" {
		clusterCIDR = "10.100.0.0/16"
	}
	if _, _, err := net.ParseCIDR(clusterCIDR); err != nil {
		return Config{}, fmt.Errorf("FORGE_NETWORK_CLUSTER_CIDR must be a valid CIDR, got %q", clusterCIDR)
	}

	nodePrefix, err := positiveIntEnv("FORGE_NETWORK_NODE_PREFIX_LEN", 24)
	if err != nil {
		return Config{}, err
	}
	if nodePrefix < 8 || nodePrefix > 28 {
		return Config{}, fmt.Errorf("FORGE_NETWORK_NODE_PREFIX_LEN must be 8–28, got %d", nodePrefix)
	}

	ipv6Enabled := false
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_IPV6_ENABLED"))) {
	case "true", "1", "yes":
		ipv6Enabled = true
	}

	reclaimSecs, err := positiveIntEnv("FORGE_NETWORK_LEASE_RECLAIM_INTERVAL_S", 60)
	if err != nil {
		return Config{}, err
	}

	providerCIDRs, err := parseCIDRList(os.Getenv("FORGE_NETWORK_PROVIDER_CIDRS"))
	if err != nil {
		return Config{}, err
	}

	dockerHost := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if dockerHost == "" {
		dockerHost = "unix:///var/run/docker.sock"
	}

	dockerCheck := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_DOCKER_COLLISION_CHECK"))) {
	case "false", "0", "no":
		dockerCheck = false
	}

	wgMTU, err := positiveIntEnv("FORGE_NETWORK_WG_MTU", 1420)
	if err != nil {
		return Config{}, err
	}
	wgKeepalive, err := positiveIntEnv("FORGE_NETWORK_WG_KEEPALIVE_S", 25)
	if err != nil {
		return Config{}, err
	}
	wgTopology := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_WG_TOPOLOGY")))
	if wgTopology == "" {
		wgTopology = "mesh"
	}
	switch wgTopology {
	case "mesh", "hub":
	default:
		return Config{}, fmt.Errorf("FORGE_NETWORK_WG_TOPOLOGY must be mesh|hub, got %q", wgTopology)
	}
	wgBackend := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_WG_BACKEND")))
	if wgBackend == "" {
		wgBackend = "auto"
	}
	switch wgBackend {
	case "kernel", "userspace", "auto":
	default:
		return Config{}, fmt.Errorf("FORGE_NETWORK_WG_BACKEND must be kernel|userspace|auto, got %q", wgBackend)
	}
	rotSecs, err := positiveIntEnv("FORGE_NETWORK_WG_ROTATION_WINDOW_S", 300)
	if err != nil {
		return Config{}, err
	}

	modeDefault := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_MODE_DEFAULT")))
	if modeDefault == "" {
		modeDefault = "wireguard"
	}
	switch modeDefault {
	case "docker", "provider-private", "wireguard":
	default:
		return Config{}, fmt.Errorf("FORGE_NETWORK_MODE_DEFAULT must be docker|provider-private|wireguard, got %q", modeDefault)
	}

	policyDefault := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_NETWORK_POLICY_DEFAULT")))
	if policyDefault == "" {
		policyDefault = "allow-within-environment"
	}
	switch policyDefault {
	case "allow-within-environment", "deny-all":
	default:
		return Config{}, fmt.Errorf("FORGE_NETWORK_POLICY_DEFAULT must be allow-within-environment|deny-all, got %q", policyDefault)
	}

	return Config{
		Port:                   port,
		ServiceName:            name,
		ServiceVersion:         version,
		LogLevel:               level,
		Env:                    env,
		ShutdownGrace:          time.Duration(graceSecs) * time.Second,
		DatabaseURL:            dbURL,
		DatabaseSchema:         schema,
		DatabasePoolMax:        poolMax,
		DatabaseMigrateOnStart: migrateOnStart,
		ClusterCIDR:            clusterCIDR,
		NodePrefixLength:       nodePrefix,
		IPv6Enabled:            ipv6Enabled,
		LeaseReclaimInterval:   time.Duration(reclaimSecs) * time.Second,
		ProviderCIDRs:          providerCIDRs,
		DockerHost:             dockerHost,
		DockerCollisionCheck:   dockerCheck,
		WgMTU:                  wgMTU,
		WgKeepaliveS:           wgKeepalive,
		WgTopology:             wgTopology,
		WgBackend:              wgBackend,
		WgRotationWindow:       time.Duration(rotSecs) * time.Second,
		ModeDefault:            modeDefault,
		PolicyDefault:          policyDefault,
	}, nil
}

func buildDatabaseURLFromParts() string {
	host := strings.TrimSpace(os.Getenv("FORGE_DB_HOST"))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv("FORGE_DB_PORT"))
	if port == "" {
		port = "5432"
	}
	name := strings.TrimSpace(os.Getenv("FORGE_DB_NAME"))
	if name == "" {
		name = "forge"
	}
	user := strings.TrimSpace(os.Getenv("FORGE_DB_USER"))
	if user == "" {
		user = "forge"
	}
	pass := strings.TrimSpace(os.Getenv("FORGE_DB_PASSWORD"))
	if pass == "" {
		pass = "forge"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, name)
}

func parseCIDRList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(p); err != nil {
			return nil, fmt.Errorf("FORGE_NETWORK_PROVIDER_CIDRS entry %q is not a valid CIDR", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func positiveIntEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return n, nil
}
