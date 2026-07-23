package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"forge.local/services/forge-infrastructure/internal/api"
	"forge.local/services/forge-infrastructure/internal/bootstraptoken"
	"forge.local/services/forge-infrastructure/internal/config"
	"forge.local/services/forge-infrastructure/internal/controller"
	"forge.local/services/forge-infrastructure/internal/health"
	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	awsprovider "forge.local/services/forge-infrastructure/internal/provider/aws"
	azureprovider "forge.local/services/forge-infrastructure/internal/provider/azure"
	"forge.local/services/forge-infrastructure/internal/provider/baremetal"
	"forge.local/services/forge-infrastructure/internal/provider/docker"
	"forge.local/services/forge-infrastructure/internal/provider/hetzner"
	"forge.local/services/forge-infrastructure/internal/provider/inventory"
	"forge.local/services/forge-infrastructure/internal/provider/noop"
	"forge.local/services/forge-infrastructure/internal/provider/ssh"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.ServiceName, cfg.LogLevel)

	ctx := context.Background()
	db, err := operations.Open(ctx, cfg.DatabaseURL, cfg.DatabaseSchema, cfg.DatabasePoolMax, cfg.DatabaseMigrateOnStart)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	ledger := &operations.Ledger{
		Pool:   db.Pool,
		IDs:    operations.NewGenerator(),
		Schema: cfg.DatabaseSchema,
	}

	reg := provider.NewRegistry(noop.Factory)
	dockerCfg := docker.Config{
		Socket:      cfg.DockerSocket,
		Network:     cfg.DockerNetwork,
		Image:       cfg.DockerImage,
		HostAddress: cfg.DockerHostAddress,
		ControlURL:  cfg.ControlURLForNodes,
		Log:         log,
	}
	dockerProv, dockerErr := docker.New(dockerCfg)
	if dockerErr != nil {
		log.Warn("docker provider client init failed; using factory fallback",
			"error", dockerErr.Error(),
		)
		reg.Register(provider.TypeDocker, docker.Factory(dockerCfg))
	} else {
		reg.Register(provider.TypeDocker, func(cfg map[string]any) (provider.Provider, error) {
			return dockerProv, nil
		})
	}

	invStore := inventory.NewPGStore(db.Pool, cfg.DatabaseSchema)
	sshDefaults := ssh.Config{
		ConnectTimeout: time.Duration(cfg.SSHConnectTimeoutSeconds) * time.Second,
		Store:          invStore,
		Log:            log,
		RuntimeImage:   cfg.RuntimeImage,
		ControlURL:     cfg.ControlURLForNodes,
		Secrets:        &ssh.MapSecrets{Keys: map[string][]byte{}},
	}
	reg.Register(provider.TypeSSH, ssh.Factory(sshDefaults))
	reg.Register(provider.TypeBareMetal, baremetal.Factory(sshDefaults))

	var hetznerResolver hetzner.TokenResolver = &hetzner.MapTokens{Values: map[string]string{}}
	if cfg.SecretsURL != "" {
		hetznerResolver = &hetzner.EnvFallbackTokens{Inner: &hetzner.HTTPSecrets{
			BaseURL: cfg.SecretsURL,
			Project: cfg.BootstrapOrganization,
			Env:     "default",
		}}
	} else {
		hetznerResolver = &hetzner.EnvFallbackTokens{Inner: hetznerResolver}
	}
	hetznerDefaults := hetzner.Config{
		APIBase:            cfg.HetznerAPIBase,
		MaxConcurrentOps:   cfg.HetznerMaxConcurrentOps,
		OrphanScanInterval: cfg.HetznerOrphanScanInterval,
		TokenResolver:      hetznerResolver,
		Log:                log,
	}
	reg.Register(provider.TypeHetzner, hetzner.Factory(hetznerDefaults))

	var awsResolver awsprovider.CredentialResolver = &awsprovider.MapSecrets{Values: map[string]string{}}
	if cfg.SecretsURL != "" {
		awsResolver = &awsprovider.EnvFallbackSecrets{Inner: &awsprovider.HTTPSecrets{
			BaseURL: cfg.SecretsURL,
			Project: cfg.BootstrapOrganization,
			Env:     "default",
		}}
	} else {
		awsResolver = &awsprovider.EnvFallbackSecrets{Inner: awsResolver}
	}
	awsDefaults := awsprovider.Config{
		APIBase:            cfg.AWSAPIBase,
		MaxConcurrentOps:   cfg.AWSMaxConcurrentOps,
		OrphanScanInterval: cfg.AWSOrphanScanInterval,
		CredentialResolver: awsResolver,
		Log:                log,
	}
	reg.Register(provider.TypeAWS, awsprovider.Factory(awsDefaults))

	var azureResolver azureprovider.CredentialResolver = &azureprovider.MapSecrets{Values: map[string]string{}}
	if cfg.SecretsURL != "" {
		azureResolver = &azureprovider.EnvFallbackSecrets{Inner: &azureprovider.HTTPSecrets{
			BaseURL: cfg.SecretsURL,
			Project: cfg.BootstrapOrganization,
			Env:     "default",
		}}
	} else {
		azureResolver = &azureprovider.EnvFallbackSecrets{Inner: azureResolver}
	}
	azureDefaults := azureprovider.Config{
		ARMBase:            cfg.AzureARMBase,
		MaxConcurrentOps:   cfg.AzureMaxConcurrentOps,
		OrphanScanInterval: cfg.AzureOrphanScanInterval,
		CredentialResolver: azureResolver,
		Log:                log,
	}
	reg.Register(provider.TypeAzure, azureprovider.Factory(azureDefaults))

	registryClient := registryclient.New(cfg.RegistryURL, log)
	ready := health.NewReadiness(db)

	mux := http.NewServeMux()
	health.NewHandler(ready).Register(mux)
	(&api.Handler{Ledger: ledger}).Register(mux)
	(&inventory.AdmissionHandler{Lister: &admissionLister{client: registryClient}}).Register(mux)

	timers := &controller.PGTimers{Pool: db.Pool, Schema: cfg.DatabaseSchema}
	tokenClient := bootstraptoken.New(cfg.BootstrapTokenURL, cfg.BootstrapOrganization, cfg.AuthMode)
	var eventPub controller.EventPublisher
	if cfg.EventsURL != "" {
		eventPub = &controller.HTTPEvents{BaseURL: cfg.EventsURL, Source: cfg.ServiceName}
	} else {
		eventPub = &controller.MemoryEvents{}
	}

	nodeCtrl := &controller.NodeController{
		Registry: registryClient,
		Ledger:   ledger,
		Timers:   timers,
		Drain:    controller.NewControlDrainHook(cfg.ControlURLForNodes),
		Events:   eventPub,
		Machines: controller.ProviderMachineObserver{},
		Health:   &controller.HTTPHealthProber{},
		Join:     &controller.ControlJoinObserver{ControlURL: cfg.ControlURLForNodes},
		Timeouts: controller.NodeTimeouts{
			Provision: time.Duration(cfg.ProvisionTimeoutSeconds) * time.Second,
			Bootstrap: time.Duration(cfg.BootstrapTimeoutSeconds) * time.Second,
			Join:      time.Duration(cfg.JoinTimeoutSeconds) * time.Second,
			Drain:     time.Duration(cfg.DrainTimeoutSeconds) * time.Second,
		},
		Log: log,
	}

	ctrl := &controller.NodePoolController{
		Registry:     registryClient,
		Ledger:       ledger,
		Providers:    reg,
		Nodes:        nodeCtrl,
		Tokens:       tokenClient,
		Log:          log,
		Interval:     cfg.ReconcileInterval,
		ControlURL:   cfg.ControlURLForNodes,
		RuntimeImage: cfg.RuntimeImage,
	}
	nodeCtrl.ResolveProvider = ctrl.ResolveProviderForPool

	orphan := &docker.OrphanReconciler{
		Provider: dockerProv,
		Known:    &docker.RegistryKnown{Lister: &registryNodeLister{client: registryClient}},
		Log:      log,
		Interval: cfg.OrphanScanInterval,
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			errCh <- err
			return
		}
		log.Info("infrastructure listening",
			"event", "infra.started",
			"port", cfg.Port,
			"registry_url", cfg.RegistryURL,
			"db_schema", cfg.DatabaseSchema,
		)
		errCh <- srv.Serve(ln)
	}()

	// Register kinds after listen so /health/live answers during Control backoff.
	regCtx, regCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer regCancel()
	if err := registryClient.RegisterKinds(regCtx, 30*time.Second); err != nil {
		log.Error("kind registration failed; readiness will stay 503",
			"event", "infra.kind_registered",
			"error", err.Error(),
		)
	} else {
		ready.MarkKindsRegistered()
		go ctrl.Run(bgCtx)
		if dockerProv != nil {
			go orphan.Run(bgCtx)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		log.Info("shutdown signal", "signal", sig.String())
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	bgCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

type registryNodeLister struct {
	client *registryclient.Client
}

type admissionLister struct {
	client *registryclient.Client
}

func (a *admissionLister) List(ctx context.Context, plural, labelSelector string) ([]inventory.AdmissionResource, error) {
	items, err := a.client.List(ctx, plural, labelSelector)
	if err != nil {
		return nil, err
	}
	out := make([]inventory.AdmissionResource, 0, len(items))
	for _, it := range items {
		typeName := ""
		cfg := map[string]any{}
		if it.Spec != nil {
			if v, ok := it.Spec["type"].(string); ok {
				typeName = v
			}
			if c, ok := it.Spec["config"].(map[string]any); ok && c != nil {
				cfg = c
			}
		}
		out = append(out, inventory.AdmissionResource{
			Name: it.Metadata.Name,
			Type: typeName,
			Cfg:  cfg,
		})
	}
	return out, nil
}

func (r *registryNodeLister) ListNodes(ctx context.Context) ([]docker.NodeResource, error) {
	items, err := r.client.List(ctx, registryclient.NodePlural, "")
	if err != nil {
		return nil, err
	}
	out := make([]docker.NodeResource, 0, len(items))
	for _, n := range items {
		id := ""
		if n.Spec != nil {
			if v, ok := n.Spec["providerNodeId"].(string); ok {
				id = v
			} else if v, ok := n.Spec["providerNodeId"]; ok && v != nil {
				id = fmt.Sprint(v)
			}
		}
		out = append(out, docker.NodeResource{ProviderNodeID: id})
	}
	return out, nil
}

func newLogger(service, level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "timestamp", Value: slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))}
			}
			return a
		},
	})
	return slog.New(h).With("service", service)
}
