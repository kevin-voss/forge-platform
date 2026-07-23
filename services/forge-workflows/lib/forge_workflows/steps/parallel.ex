defmodule ForgeWorkflows.Steps.Parallel do
  @moduledoc false

  alias ForgeWorkflows.Engine.StepExecutor
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Runs

  @doc """
  Fan-out/fan-in: run branch step defs concurrently (capped), join results.

  Default policy is *collect then fail* if any required branch failed.
  """
  @spec run(String.t(), String.t(), [map()], map(), keyword()) ::
          {:ok, map()} | {:error, String.t()}
  def run(run_id, parent_step_id, branches, context, opts \\ [])
      when is_binary(run_id) and is_binary(parent_step_id) and is_list(branches) do
    max = Keyword.get(opts, :max_parallelism, 8)
    default_timeout = Keyword.get(opts, :default_step_timeout_ms, 300_000)

    Metrics.inc_parallel_branches(length(branches))

    log("parallel fan-out", %{
      run_id: run_id,
      step_id: parent_step_id,
      branches: Enum.map(branches, &branch_id/1),
      max_parallelism: max
    })

    Enum.each(branches, fn branch ->
      _ = Runs.ensure_child_step(run_id, parent_step_id, branch_id(branch), branch_type(branch))
    end)

    results =
      branches
      |> Task.async_stream(
        fn branch ->
          execute_branch(run_id, parent_step_id, branch, context, default_timeout)
        end,
        max_concurrency: max,
        timeout: :infinity,
        ordered: true
      )
      |> Enum.map(fn
        {:ok, result} -> result
        {:exit, reason} -> {:error, branch_id_unknown(), "branch crashed: #{inspect(reason)}"}
      end)

    branches_out =
      Map.new(results, fn
        {:ok, id, output} -> {id, %{"status" => "completed", "output" => output}}
        {:error, id, reason} -> {id, %{"status" => "failed", "error" => reason}}
      end)

    failed =
      results
      |> Enum.filter(fn
        {:error, _, _} -> true
        _ -> false
      end)

    log("parallel join", %{
      run_id: run_id,
      step_id: parent_step_id,
      branch_count: length(branches),
      failed: length(failed)
    })

    if failed == [] do
      {:ok, %{"branches" => branches_out, "policy" => "collect"}}
    else
      reasons =
        Enum.map(failed, fn {:error, id, reason} -> "#{id}: #{reason}" end)
        |> Enum.join("; ")

      {:error, "parallel branch failure: #{reasons}"}
    end
  end

  @spec join_results([{String.t(), {:ok, map()} | {:error, String.t()}}]) ::
          {:ok, map()} | {:error, String.t()}
  def join_results(pairs) when is_list(pairs) do
    branches =
      Map.new(pairs, fn
        {id, {:ok, output}} -> {id, %{"status" => "completed", "output" => output}}
        {id, {:error, reason}} -> {id, %{"status" => "failed", "error" => reason}}
      end)

    failed =
      Enum.filter(pairs, fn
        {_, {:error, _}} -> true
        _ -> false
      end)

    if failed == [] do
      {:ok, %{"branches" => branches, "policy" => "collect"}}
    else
      reasons =
        Enum.map(failed, fn {id, {:error, reason}} -> "#{id}: #{reason}" end)
        |> Enum.join("; ")

      {:error, "parallel branch failure: #{reasons}"}
    end
  end

  defp execute_branch(run_id, _parent_step_id, branch, context, default_timeout) do
    id = branch_id(branch)

    case Runs.begin_step(run_id, id) do
      {:ok, :skip, step} ->
        {:ok, id, step.output || %{"skipped" => true}}

      {:ok, :execute, step} ->
        case StepExecutor.execute(branch, context,
               attempt: step.attempt,
               timeout_ms: default_timeout
             ) do
          {:ok, output} ->
            _ = Runs.complete_step(run_id, id, output)
            {:ok, id, output}

          {:error, reason} ->
            _ = Runs.fail_step(run_id, id, reason)
            {:error, id, reason}
        end

      {:ok, :wake, step} ->
        # Child should not be waiting under parallel fan-out in 16.03.
        {:error, id, "unexpected waiting child status=#{step.status}"}

      {:error, reason} ->
        {:error, id, "begin_step failed: #{inspect(reason)}"}
    end
  end

  defp branch_id(branch), do: Map.get(branch, :id) || Map.get(branch, "id") || "unknown"
  defp branch_type(branch), do: Map.get(branch, :type) || Map.get(branch, "type") || "noop"
  defp branch_id_unknown, do: "unknown"

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
