import Config

# ExUnit boots the OTP app; unit tests inject config without binding a port.
config :forge_workflows, start_http: false

config :forge_workflows, ForgeWorkflows.Repo,
  pool: Ecto.Adapters.SQL.Sandbox,
  pool_size: 5
