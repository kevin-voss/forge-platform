import Config

# ExUnit boots the OTP app; router/health tests inject config without binding a port.
config :forge_workflows, start_http: false
