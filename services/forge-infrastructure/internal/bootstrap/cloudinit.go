package bootstrap

import "fmt"

// RenderCloudInit produces a #cloud-config document for VM providers (23.05/23.06).
func RenderCloudInit(p Payload) string {
	return fmt.Sprintf(`#cloud-config
write_files:
  - path: /etc/forge/runtime.env
    content: |
      FORGE_CONTROL_URL=%s
      FORGE_BOOTSTRAP_TOKEN=%s
runcmd:
  - curl -fsSL https://get.docker.com | sh
  - docker run -d --name forge-runtime --env-file /etc/forge/runtime.env \
      %s
`, p.ControlURL, p.BootstrapToken, p.RuntimeImage)
}
