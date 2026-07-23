import Config

# ExUnit boots the OTP app; router tests inject config without binding a port.
config :notify_elixir, start_http: false
