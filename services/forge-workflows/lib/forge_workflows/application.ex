defmodule ForgeWorkflows.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    children =
      if Application.get_env(:forge_workflows, :start_http, true) do
        cfg = ForgeWorkflows.Config.load!()
        Application.put_env(:forge_workflows, :runtime_config, cfg)
        Application.put_env(:forge_workflows, :started_at, System.monotonic_time(:second))

        ForgeWorkflows.JsonLog.info(cfg.service_name, "starting forge-workflows", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env,
          supervision: ["RunRegistry", "RunSupervisor", "Bandit"]
        })

        ForgeWorkflows.JsonLog.info(cfg.service_name, "listening", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env
        })

        [
          {Registry, keys: :unique, name: ForgeWorkflows.RunRegistry},
          {DynamicSupervisor, name: ForgeWorkflows.RunSupervisor, strategy: :one_for_one},
          {Bandit,
           plug: ForgeWorkflowsWeb.Router,
           scheme: :http,
           port: cfg.port,
           ip: {0, 0, 0, 0},
           thousand_island_options: [shutdown_timeout: cfg.shutdown_grace_ms]}
        ]
      else
        []
      end

    opts = [strategy: :one_for_one, name: ForgeWorkflows.Supervisor]
    Supervisor.start_link(children, opts)
  end

  @impl true
  def stop(_state) do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{service_name: name} ->
        ForgeWorkflows.JsonLog.info(name, "shutdown signal received", %{signal: "SIGTERM"})
        ForgeWorkflows.JsonLog.info(name, "shutdown complete")

      _ ->
        :ok
    end

    :ok
  end
end
