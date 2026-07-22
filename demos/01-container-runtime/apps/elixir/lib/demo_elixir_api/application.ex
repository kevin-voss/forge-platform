defmodule DemoElixirApi.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    children =
      if Application.get_env(:demo_elixir_api, :start_http, true) do
        cfg = DemoElixirApi.Config.load!()
        Application.put_env(:demo_elixir_api, :runtime_config, cfg)
        Application.put_env(:demo_elixir_api, :started_at, System.monotonic_time(:second))

        DemoElixirApi.JsonLog.info(cfg.service_name, "listening", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env
        })

        [
          {Bandit,
           plug: DemoElixirApi.Router,
           scheme: :http,
           port: cfg.port,
           ip: {0, 0, 0, 0},
           thousand_island_options: [shutdown_timeout: 5_000]}
        ]
      else
        []
      end

    opts = [strategy: :one_for_one, name: DemoElixirApi.Supervisor]
    Supervisor.start_link(children, opts)
  end

  @impl true
  def stop(_state) do
    case Application.get_env(:demo_elixir_api, :runtime_config) do
      %{service_name: name} ->
        DemoElixirApi.JsonLog.info(name, "shutdown signal received", %{signal: "SIGTERM"})
        DemoElixirApi.JsonLog.info(name, "shutdown complete")

      _ ->
        :ok
    end

    :ok
  end
end
