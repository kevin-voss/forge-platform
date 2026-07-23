package bootstrap

import "fmt"

// RenderSSHScript produces an install script for SSH/reachable-host providers (23.04).
// The caller copies this script + EnvFile() over SSH, executes it, then polls health.
func RenderSSHScript(p Payload) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
mkdir -p /etc/forge
cat > /etc/forge/runtime.env <<'EOF'
%sEOF
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | sh
fi
docker rm -f forge-runtime >/dev/null 2>&1 || true
docker run -d --name forge-runtime --env-file /etc/forge/runtime.env \
  %s
`, p.EnvFile(), p.RuntimeImage)
}
