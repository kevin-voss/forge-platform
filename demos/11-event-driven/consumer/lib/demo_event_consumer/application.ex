defmodule DemoEventConsumer.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    children =
      if Application.get_env(:demo_event_consumer, :start_http, true) do
        cfg = DemoEventConsumer.Config.load!()
        Application.put_env(:demo_event_consumer, :runtime_config, cfg)
        Application.put_env(:demo_event_consumer, :started_at, System.monotonic_time(:second))

        {:ok, _} = Application.ensure_all_started(:inets)

        DemoEventConsumer.JsonLog.info(cfg.service_name, "listening", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env,
          events_url: cfg.events_url
        })

        [
          {DemoEventConsumer.Worker, cfg},
          {Bandit,
           plug: DemoEventConsumer.Router,
           scheme: :http,
           port: cfg.port,
           ip: {0, 0, 0, 0},
           thousand_island_options: [shutdown_timeout: 5_000]}
        ]
      else
        []
      end

    opts = [strategy: :one_for_one, name: DemoEventConsumer.Supervisor]
    Supervisor.start_link(children, opts)
  end

  @impl true
  def stop(_state) do
    case Application.get_env(:demo_event_consumer, :runtime_config) do
      %{service_name: name} ->
        DemoEventConsumer.JsonLog.info(name, "shutdown signal received", %{signal: "SIGTERM"})
        DemoEventConsumer.JsonLog.info(name, "shutdown complete")

      _ ->
        :ok
    end

    :ok
  end
end
