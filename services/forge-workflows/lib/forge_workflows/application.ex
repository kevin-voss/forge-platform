defmodule ForgeWorkflows.Application do
  @moduledoc false

  use Application

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Engine.BootResumer
  alias ForgeWorkflows.Engine.RunSupervisor
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Repo

  @impl true
  def start(_type, _args) do
    children =
      if Application.get_env(:forge_workflows, :start_http, true) do
        cfg = ForgeWorkflows.Config.load!()
        Application.put_env(:forge_workflows, :runtime_config, cfg)
        Application.put_env(:forge_workflows, :started_at, System.monotonic_time(:second))

        Application.put_env(
          :forge_workflows,
          ForgeWorkflows.Repo,
          url: cfg.database_url,
          pool_size: String.to_integer(System.get_env("FORGE_WORKFLOWS_POOL_SIZE") || "5")
        )


        Metrics.ensure_table!()

        definitions = Loader.load_dir!(cfg.defs_dir)
        Loader.put_definitions(definitions)

        ForgeWorkflows.JsonLog.info(cfg.service_name, "starting forge-workflows", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env,
          defs_dir: cfg.defs_dir,
          definitions: map_size(definitions),
          supervision: [
            "Repo",
            "Migrator",
            "RunRegistry",
            "RunSupervisor",
            "BootResumer",
            "Bandit"
          ]
        })

        [
          Repo,
          {Ecto.Migrator,
           repos: [Repo],
           skip: System.get_env("FORGE_WORKFLOWS_SKIP_MIGRATIONS") in ["1", "true"]},
          {Registry, keys: :unique, name: ForgeWorkflows.RunRegistry},
          RunSupervisor,
          BootResumer,
          {Bandit,
           plug: ForgeWorkflowsWeb.Router,
           scheme: :http,
           port: cfg.port,
           ip: {0, 0, 0, 0},
           thousand_island_options: [shutdown_timeout: cfg.shutdown_grace_ms]}
        ]
      else
        Metrics.ensure_table!()
        []
      end

    opts = [strategy: :rest_for_one, name: ForgeWorkflows.Supervisor]
    result = Supervisor.start_link(children, opts)

    if Application.get_env(:forge_workflows, :start_http, true) do
      cfg = Application.fetch_env!(:forge_workflows, :runtime_config)

      ForgeWorkflows.JsonLog.info(cfg.service_name, "listening", %{
        port: cfg.port,
        version: cfg.service_version,
        env: cfg.env
      })
    end

    result
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
