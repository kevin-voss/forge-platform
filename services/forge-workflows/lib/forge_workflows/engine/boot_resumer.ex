defmodule ForgeWorkflows.Engine.BootResumer do
  @moduledoc false

  use GenServer

  alias ForgeWorkflows.Engine.RunSupervisor
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Runs

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    send(self(), :resume)
    {:ok, %{}}
  end

  @impl true
  def handle_info(:resume, state) do
    ids = Runs.list_inflight_run_ids()

    Enum.each(ids, fn run_id ->
      case RunSupervisor.start_run(run_id) do
        {:ok, _pid} ->
          log("resumed in-flight run", %{run_id: run_id})

        {:error, reason} ->
          log("failed to resume run", %{run_id: run_id, error: inspect(reason)})
      end
    end)

    log("boot resume complete", %{resumed: length(ids)})
    {:noreply, state}
  end

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
