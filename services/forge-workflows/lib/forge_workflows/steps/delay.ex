defmodule ForgeWorkflows.Steps.Delay do
  @moduledoc false

  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Runs

  @spec schedule(String.t(), String.t(), non_neg_integer()) ::
          {:ok, DateTime.t()} | {:error, term()}
  def schedule(run_id, step_id, delay_ms)
      when is_binary(run_id) and is_binary(step_id) and is_integer(delay_ms) and delay_ms >= 0 do
    now = DateTime.utc_now() |> DateTime.truncate(:microsecond)
    wake_at = DateTime.add(now, delay_ms, :millisecond)

    case Runs.mark_step_waiting(run_id, step_id, wake_at) do
      {:ok, _} ->
        log("delay scheduled", %{
          run_id: run_id,
          step_id: step_id,
          delay_ms: delay_ms,
          wake_at: DateTime.to_iso8601(wake_at)
        })

        {:ok, wake_at}

      other ->
        other
    end
  end

  @spec due?(DateTime.t() | nil, DateTime.t()) :: boolean()
  def due?(nil, _now), do: false

  def due?(%DateTime{} = wake_at, %DateTime{} = now) do
    DateTime.compare(wake_at, now) != :gt
  end

  @spec remaining_ms(DateTime.t(), DateTime.t()) :: non_neg_integer()
  def remaining_ms(%DateTime{} = wake_at, %DateTime{} = now) do
    case DateTime.diff(wake_at, now, :millisecond) do
      n when n > 0 -> n
      _ -> 0
    end
  end

  @spec complete(String.t(), String.t(), non_neg_integer()) :: {:ok, map()} | {:error, term()}
  def complete(run_id, step_id, delay_ms) do
    output = %{
      "delayed" => true,
      "delay_ms" => delay_ms,
      "woke_at" => DateTime.to_iso8601(DateTime.utc_now())
    }

    case Runs.complete_step(run_id, step_id, output) do
      {:ok, _} ->
        log("delay fired", %{run_id: run_id, step_id: step_id, delay_ms: delay_ms})
        {:ok, output}

      other ->
        other
    end
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
