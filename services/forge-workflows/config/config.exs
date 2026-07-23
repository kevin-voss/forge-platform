import Config

# Keep BEAM Logger silent so docker logs stay JSONL-only (JsonLog writes stdout).
config :logger,
  level: :none,
  backends: []

config :forge_workflows, start_http: true

config :forge_workflows, ForgeWorkflows.Repo,
  pool_size: 5,
  migration_timestamps: [type: :utc_datetime_usec]

config :forge_workflows, ecto_repos: [ForgeWorkflows.Repo]

import_config "#{config_env()}.exs"
