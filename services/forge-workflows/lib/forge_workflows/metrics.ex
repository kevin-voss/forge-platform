defmodule ForgeWorkflows.Metrics do
  @moduledoc false

  @table :forge_workflows_metrics

  def ensure_table! do
    case :ets.whereis(@table) do
      :undefined ->
        :ets.new(@table, [:named_table, :public, :set, write_concurrency: true])

      _ ->
        @table
    end

    :ok
  end

  def inc_run(status) when is_binary(status) do
    ensure_table!()
    :ets.update_counter(@table, {:workflow_runs_total, status}, {2, 1}, {{:workflow_runs_total, status}, 0})
    :ok
  end

  def inc_step(status) when is_binary(status) do
    ensure_table!()
    :ets.update_counter(@table, {:workflow_steps_total, status}, {2, 1}, {{:workflow_steps_total, status}, 0})
    :ok
  end

  def inc_retry do
    ensure_table!()
    :ets.update_counter(@table, :workflow_step_retries_total, {2, 1}, {:workflow_step_retries_total, 0})
    :ok
  end

  def inc_parallel_branches(n) when is_integer(n) and n >= 0 do
    ensure_table!()

    :ets.update_counter(
      @table,
      :workflow_parallel_branches,
      {2, n},
      {:workflow_parallel_branches, 0}
    )

    :ok
  end

  def inc_timeout do
    ensure_table!()
    :ets.update_counter(@table, :workflow_timeouts_total, {2, 1}, {:workflow_timeouts_total, 0})
    :ok
  end

  def snapshot do
    ensure_table!()

    :ets.tab2list(@table)
    |> Enum.map(fn {key, value} -> {key, value} end)
    |> Map.new()
  end
end
