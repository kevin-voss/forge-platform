import Config

# Keep BEAM Logger silent so docker logs stay JSONL-only (JsonLog writes stdout).
config :logger,
  level: :none,
  backends: []

config :notify_elixir, start_http: true

import_config "#{config_env()}.exs"
