defmodule ForgeWorkflows.Engine.Scheduler do
  @moduledoc false

  use GenServer

  alias ForgeWorkflows.Engine.RunServer
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Runs

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    # Boot scan immediately, then tick.
    send(self(), :tick)
    {:ok, %{}}
  end

  @impl true
  def handle_info(:tick, state) do
    fire_due()
    schedule_tick()
    {:noreply, state}
  end

  defp fire_due do
    due = Runs.list_due_waiting_steps()

    Enum.each(due, fn step ->
      log("firing due timer", %{
        run_id: step.run_id,
        step_id: step.step_id,
        wake_at: step.wake_at && DateTime.to_iso8601(step.wake_at)
      })

      RunServer.wake(step.run_id, step.step_id)
    end)
  end

  defp schedule_tick do
    tick =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{scheduler_tick_ms: ms} when is_integer(ms) and ms >= 1 -> ms
        _ -> 1_000
      end

    Process.send_after(self(), :tick, tick)
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
