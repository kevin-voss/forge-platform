import Config

config :demo_event_consumer,
  start_http: true

config :logger, level: :info

import_config "#{config_env()}.exs"