defmodule ForgeWorkflows.Steps.Timeout do
  @moduledoc false

  alias ForgeWorkflows.Metrics

  @spec step_timeout_ms(map(), pos_integer()) :: pos_integer()
  def step_timeout_ms(step_def, default) when is_map(step_def) and is_integer(default) do
    case Map.get(step_def, :timeout_ms) || Map.get(step_def, "timeout_ms") do
      n when is_integer(n) and n >= 1 -> n
      _ -> default
    end
  end

  @spec run_expired?(DateTime.t(), pos_integer() | nil, DateTime.t()) :: boolean()
  def run_expired?(_started_at, nil, _now), do: false

  def run_expired?(%DateTime{} = started_at, timeout_ms, %DateTime{} = now)
      when is_integer(timeout_ms) and timeout_ms >= 1 do
    deadline = DateTime.add(started_at, timeout_ms, :millisecond)
    DateTime.compare(now, deadline) == :gt
  end

  @spec execute_with_timeout((-> result), pos_integer()) :: result | {:error, String.t()}
        when result: {:ok, map()} | {:error, String.t()}
  def execute_with_timeout(fun, timeout_ms)
      when is_function(fun, 0) and is_integer(timeout_ms) and timeout_ms >= 1 do
    task = Task.async(fun)

    case Task.yield(task, timeout_ms) || Task.shutdown(task, :brutal_kill) do
      {:ok, result} ->
        result

      nil ->
        Metrics.inc_timeout()
        {:error, "timeout"}
    end
  end
end
