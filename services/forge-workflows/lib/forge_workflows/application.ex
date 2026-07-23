defmodule ForgeWorkflows.Application do
  @moduledoc false

  use Application

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Engine.BootResumer
  alias ForgeWorkflows.Engine.RunSupervisor
  alias ForgeWorkflows.Engine.Scheduler
  alias ForgeWorkflows.Events.Consumer
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

        if cfg.agents_mode != "live" do
          Application.put_env(
            :forge_workflows,
            :agent_client,
            ForgeWorkflows.Clients.AgentClient.Default
          )
        end

        if not cfg.events_enabled do
          Application.put_env(
            :forge_workflows,
            :events_client,
            ForgeWorkflows.Clients.EventsClient.Noop
          )
        end

        ForgeWorkflows.JsonLog.info(cfg.service_name, "starting forge-workflows", %{
          port: cfg.port,
          version: cfg.service_version,
          env: cfg.env,
          defs_dir: cfg.defs_dir,
          definitions: map_size(definitions),
          triggers: length(ForgeWorkflows.Triggers.Registry.event_types()),
          events_enabled: cfg.events_enabled,
          agents_mode: cfg.agents_mode,
          supervision: [
            "Repo",
            "Migrator",
            "RunRegistry",
            "RunSupervisor",
            "BootResumer",
            "Scheduler",
            "EventConsumer",
            "Bandit"
          ],
          max_parallelism: cfg.max_parallelism,
          default_step_timeout_ms: cfg.default_step_timeout_ms,
          scheduler_tick_ms: cfg.scheduler_tick_ms,
          agent_poll_ms: cfg.agent_poll_ms,
          agent_step_timeout_ms: cfg.agent_step_timeout_ms
        })

        [
          Repo,
          {Ecto.Migrator,
           repos: [Repo],
           skip: System.get_env("FORGE_WORKFLOWS_SKIP_MIGRATIONS") in ["1", "true"]},
          {Registry, keys: :unique, name: ForgeWorkflows.RunRegistry},
          RunSupervisor,
          BootResumer,
          Scheduler,
          {Consumer, enabled: cfg.events_enabled},
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
