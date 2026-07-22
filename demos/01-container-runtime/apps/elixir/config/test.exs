import Config

# ExUnit boots the OTP app; router tests inject config without binding a port.
config :demo_elixir_api, start_http: false
