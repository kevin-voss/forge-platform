package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-infrastructure.
type Config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	AuthMode       string
	ShutdownGrace  time.Duration

	DatabaseURL            string
	DatabaseSchema         string
	DatabasePoolMax        int
	DatabaseMigrateOnStart bool

	RegistryURL       string
	ReconcileInterval time.Duration

	DockerSocket       string
	DockerNetwork      string
	DockerImage        string
	DockerHostAddress  string
	OrphanScanInterval time.Duration
	ControlURLForNodes string

	ProvisionTimeoutSeconds int
	BootstrapTimeoutSeconds int
	JoinTimeoutSeconds      int
	DrainTimeoutSeconds     int
	BootstrapTokenURL       string
	BootstrapOrganization   string
	RuntimeImage            string
	EventsURL               string

	SSHConnectTimeoutSeconds int
	SSHProbeIntervalSeconds  int
	SecretsURL               string

	HetznerAPIBase            string
	HetznerMaxConcurrentOps   int
	HetznerOrphanScanInterval time.Duration
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
		name = "forge-infrastructure"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}
	authMode := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTH_MODE")))
	if authMode == "" {
		authMode = "dev"
	}

	graceRaw := strings.TrimSpace(os.Getenv("FORGE_SHUTDOWN_GRACE_SECONDS"))
	if graceRaw == "" {
		graceRaw = "10"
	}
	graceSecs, err := strconv.Atoi(graceRaw)
	if err != nil || graceSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got %q", graceRaw)
	}

	dbURL := strings.TrimSpace(os.Getenv("FORGE_INFRA_DB_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("FORGE_DATABASE_URL"))
	}
	if dbURL == "" {
		dbURL = "postgres://forge:forge@localhost:5432/forge?sslmode=disable"
	}
	schema := strings.TrimSpace(os.Getenv("FORGE_DATABASE_SCHEMA"))
	if schema == "" {
		schema = "infrastructure"
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_DATABASE_MIGRATE_ON_START"))) {
	case "false", "0", "no":
		migrateOnStart = false
	}

	registryURL := strings.TrimSpace(os.Getenv("FORGE_REGISTRY_URL"))
	if registryURL == "" {
		registryURL = strings.TrimSpace(os.Getenv("FORGE_CONTROL_URL"))
	}
	if registryURL == "" {
		registryURL = "http://127.0.0.1:4001"
	}

	intervalRaw := strings.TrimSpace(os.Getenv("FORGE_INFRA_RECONCILE_INTERVAL_MS"))
	if intervalRaw == "" {
		intervalRaw = "2000"
	}
	intervalMs, err := strconv.Atoi(intervalRaw)
	if err != nil || intervalMs < 100 {
		return Config{}, fmt.Errorf("FORGE_INFRA_RECONCILE_INTERVAL_MS must be >= 100, got %q", intervalRaw)
	}

	dockerSocket := strings.TrimSpace(os.Getenv("FORGE_INFRA_DOCKER_SOCKET"))
	if dockerSocket == "" {
		dockerSocket = strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	}
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}
	dockerNetwork := strings.TrimSpace(os.Getenv("FORGE_INFRA_DOCKER_NETWORK"))
	if dockerNetwork == "" {
		dockerNetwork = "forge-platform_default"
	}
	dockerImage := strings.TrimSpace(os.Getenv("FORGE_INFRA_DOCKER_IMAGE"))
	if dockerImage == "" {
		dockerImage = "forge/forge-runtime:local"
	}
	dockerHostAddr := strings.TrimSpace(os.Getenv("FORGE_INFRA_DOCKER_HOST_ADDRESS"))
	if dockerHostAddr == "" {
		dockerHostAddr = "127.0.0.1"
	}
	orphanRaw := strings.TrimSpace(os.Getenv("FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S"))
	if orphanRaw == "" {
		orphanRaw = "30"
	}
	orphanSecs, err := strconv.Atoi(orphanRaw)
	if err != nil || orphanSecs < 1 {
		return Config{}, fmt.Errorf("FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S must be >= 1, got %q", orphanRaw)
	}
	controlURLNodes := strings.TrimSpace(os.Getenv("FORGE_CONTROL_URL"))
	if controlURLNodes == "" {
		controlURLNodes = "http://forge-control:8080"
	}

	provisionTO, err := envIntDefault("FORGE_NODE_PROVISION_TIMEOUT_SECONDS", 180)
	if err != nil {
		return Config{}, err
	}
	bootstrapTO, err := envIntDefault("FORGE_NODE_BOOTSTRAP_TIMEOUT_SECONDS", 600)
	if err != nil {
		return Config{}, err
	}
	joinTO, err := envIntDefault("FORGE_NODE_JOIN_TIMEOUT_SECONDS", 120)
	if err != nil {
		return Config{}, err
	}
	drainTO, err := envIntDefault("FORGE_NODE_DRAIN_TIMEOUT_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	tokenURL := strings.TrimSpace(os.Getenv("FORGE_BOOTSTRAP_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = strings.TrimRight(controlURLNodes, "/")
	}
	org := strings.TrimSpace(os.Getenv("FORGE_BOOTSTRAP_ORGANIZATION"))
	if org == "" {
		org = "forge"
	}
	runtimeImage := strings.TrimSpace(os.Getenv("FORGE_RUNTIME_IMAGE"))
	if runtimeImage == "" {
		runtimeImage = dockerImage
	}
	eventsURL := strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL"))

	sshConnectTO, err := envIntDefault("FORGE_INFRA_SSH_CONNECT_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	sshProbeInterval, err := envIntDefault("FORGE_INFRA_SSH_PROBE_INTERVAL_SECONDS", 60)
	if err != nil {
		return Config{}, err
	}
	secretsURL := strings.TrimSpace(os.Getenv("FORGE_SECRETS_URL"))

	hetznerBase := strings.TrimSpace(os.Getenv("FORGE_INFRA_HETZNER_API_BASE"))
	if hetznerBase == "" {
		hetznerBase = "https://api.hetzner.cloud/v1"
	}
	hetznerMaxConc, err := envIntDefault("FORGE_INFRA_HETZNER_MAX_CONCURRENT_OPS", 5)
	if err != nil {
		return Config{}, err
	}
	hetznerOrphanRaw := strings.TrimSpace(os.Getenv("FORGE_INFRA_HETZNER_ORPHAN_SCAN_INTERVAL_S"))
	if hetznerOrphanRaw == "" {
		hetznerOrphanRaw = "300"
	}
	hetznerOrphanSecs, err := strconv.Atoi(hetznerOrphanRaw)
	if err != nil || hetznerOrphanSecs < 1 {
		return Config{}, fmt.Errorf("FORGE_INFRA_HETZNER_ORPHAN_SCAN_INTERVAL_S must be >= 1, got %q", hetznerOrphanRaw)
	}

	return Config{
		Port:                      port,
		ServiceName:               name,
		ServiceVersion:            version,
		LogLevel:                  level,
		Env:                       env,
		AuthMode:                  authMode,
		ShutdownGrace:             time.Duration(graceSecs) * time.Second,
		DatabaseURL:               dbURL,
		DatabaseSchema:            schema,
		DatabasePoolMax:           poolMax,
		DatabaseMigrateOnStart:    migrateOnStart,
		RegistryURL:               strings.TrimRight(registryURL, "/"),
		ReconcileInterval:         time.Duration(intervalMs) * time.Millisecond,
		DockerSocket:              dockerSocket,
		DockerNetwork:             dockerNetwork,
		DockerImage:               dockerImage,
		DockerHostAddress:         dockerHostAddr,
		OrphanScanInterval:        time.Duration(orphanSecs) * time.Second,
		ControlURLForNodes:        strings.TrimRight(controlURLNodes, "/"),
		ProvisionTimeoutSeconds:   provisionTO,
		BootstrapTimeoutSeconds:   bootstrapTO,
		JoinTimeoutSeconds:        joinTO,
		DrainTimeoutSeconds:       drainTO,
		BootstrapTokenURL:         strings.TrimRight(tokenURL, "/"),
		BootstrapOrganization:     org,
		RuntimeImage:              runtimeImage,
		EventsURL:                 strings.TrimRight(eventsURL, "/"),
		SSHConnectTimeoutSeconds:  sshConnectTO,
		SSHProbeIntervalSeconds:   sshProbeInterval,
		SecretsURL:                strings.TrimRight(secretsURL, "/"),
		HetznerAPIBase:            strings.TrimRight(hetznerBase, "/"),
		HetznerMaxConcurrentOps:   hetznerMaxConc,
		HetznerOrphanScanInterval: time.Duration(hetznerOrphanSecs) * time.Second,
	}, nil
}

func envIntDefault(key string, def int) (int, error) {
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
