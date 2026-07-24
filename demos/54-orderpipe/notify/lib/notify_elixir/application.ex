defmodule NotifyElixir.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    _ = Application.ensure_all_started(:inets)
    _ = Application.ensure_all_started(:ssl)

    children =
      if Application.get_env(:notify_elixir, :start_http, true) do
        cfg = NotifyElixir.Config.load!()
        Application.put_env(:notify_elixir, :runtime_config, cfg)
        Application.put_env(:notify_elixir, :started_at, System.monotonic_time(:second))

        NotifyElixir.JsonLog.info(cfg.service_name, "listening", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env,
          events: cfg.events_url,
          consumer: cfg.consumer_name,
          subject: cfg.consume_subject
        })

        [
          NotifyElixir.Store,
          {NotifyElixir.Worker, cfg},
          {Bandit,
           plug: NotifyElixir.Router,
           scheme: :http,
           port: cfg.port,
           ip: {0, 0, 0, 0},
           thousand_island_options: [shutdown_timeout: 5_000]}
        ]
      else
        [NotifyElixir.Store]
      end

    opts = [strategy: :one_for_one, name: NotifyElixir.Supervisor]
    Supervisor.start_link(children, opts)
  end

  @impl true
  def stop(_state) do
    case Application.get_env(:notify_elixir, :runtime_config) do
      %{service_name: name} ->
        NotifyElixir.JsonLog.info(name, "shutdown signal received", %{signal: "SIGTERM"})
        NotifyElixir.JsonLog.info(name, "shutdown complete")

      _ ->
        :ok
    end

    :ok
  end
end
