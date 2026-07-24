package main

import (
	"os"
	"strings"
)

// peerConfig holds Discovery-backed peer addressing for fulfillment/notify.
// Defaults use *.svc.forge names — never compose service DNS or raw IPs.
type peerConfig struct {
	FulfillmentURL string
	NotifyURL      string
	DiscoveryURL   string
	Project        string
	Environment    string
}

func loadPeerConfig() peerConfig {
	return peerConfig{
		FulfillmentURL: envOr("FULFILLMENT_URL", "http://fulfillment.svc.forge:8080"),
		NotifyURL:      envOr("NOTIFY_URL", "http://notify.svc.forge:8080"),
		DiscoveryURL:   envOr("FORGE_DISCOVERY_URL", "http://host.docker.internal:4109"),
		Project:        envOr("FORGE_DISCOVERY_PROJECT", "orderpipe"),
		Environment:    envOr("FORGE_DISCOVERY_ENVIRONMENT", "local"),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
