package bootstrap_test

import (
	"strings"
	"testing"

	"forge.local/services/forge-infrastructure/internal/bootstrap"
)

func TestPayloadRendersCloudInitAndSSHWithoutTokenInLogs(t *testing.T) {
	p := bootstrap.Payload{
		ControlURL:     "http://forge-control:8080",
		BootstrapToken: "bst_super_secret_token_9f2a",
		NodePool:       "local-hetzner-pool",
		RuntimeImage:   "registry.forge.internal/forge/forge-runtime:1.4.0",
	}

	cloud := bootstrap.RenderCloudInit(p)
	ssh := bootstrap.RenderSSHScript(p)
	core := p.CoreYAML()

	if !strings.Contains(cloud, "#cloud-config") {
		t.Fatalf("cloud-init missing header: %s", cloud)
	}
	if !strings.Contains(cloud, p.BootstrapToken) {
		t.Fatal("cloud-init should embed token for the node")
	}
	if !strings.Contains(ssh, "#!/bin/sh") {
		t.Fatalf("ssh script missing shebang: %s", ssh)
	}
	if !strings.Contains(ssh, p.RuntimeImage) || !strings.Contains(cloud, p.RuntimeImage) {
		t.Fatal("both renderers must share runtime image from payload")
	}
	if !strings.Contains(core, "forge_bootstrap:") {
		t.Fatalf("core yaml missing forge_bootstrap: %s", core)
	}

	logLine := formatLog(p.LogSafe())
	if bootstrap.ContainsRawToken(logLine, p.BootstrapToken) {
		t.Fatalf("token leaked into log-formatted output: %s", logLine)
	}
	if !strings.Contains(logLine, "***") {
		t.Fatalf("expected masked token in log output: %s", logLine)
	}
}

func formatLog(m map[string]string) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte(' ')
	}
	return b.String()
}
