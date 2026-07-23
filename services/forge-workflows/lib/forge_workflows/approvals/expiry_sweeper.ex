defmodule ForgeWorkflows.Approvals.ExpirySweeper do
  @moduledoc false

  use GenServer

  alias ForgeWorkflows.Approvals.Store
  alias ForgeWorkflows.Engine.RunServer
  alias ForgeWorkflows.JsonLog

  @default_tick_ms 5_000

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(opts) do
    tick_ms = Keyword.get(opts, :tick_ms, @default_tick_ms)
    state = %{tick_ms: tick_ms}
    schedule_tick(tick_ms)
    {:ok, state}
  end

  @impl true
  def handle_info(:tick, state) do
    expire_due()
    schedule_tick(state.tick_ms)
    {:noreply, state}
  end

  def handle_info(_msg, state), do: {:noreply, state}

  defp expire_due do
    Store.list_expired_pending()
    |> Enum.each(fn approval ->
      case Store.expire(approval) do
        {:ok, expired} ->
          log("expired pending approval", %{
            approval_id: expired.id,
            run_id: expired.run_id,
            step_id: expired.step_id
          })

          _ = RunServer.wake(expired.run_id, expired.step_id)

        {:error, reason} ->
          log("failed to expire approval", %{
            approval_id: approval.id,
            error: inspect(reason)
          })
      end
    end)
  end

  defp schedule_tick(ms), do: Process.send_after(self(), :tick, ms)

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
