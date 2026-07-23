defmodule ForgeWorkflows.Saga.Compensator do
  @moduledoc false

  alias ForgeWorkflows.Clients.ControlClient
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Saga.Log, as: SagaLog
  alias ForgeWorkflows.Steps.Report

  @doc """
  Execute pending/running saga compensators in reverse order (idempotent).

  Individual compensator failures are recorded; remaining entries are still
  attempted. Returns `{:ok, report}` when every entry ends compensated,
  otherwise `{:error, report}`.
  """
  @spec run(String.t(), keyword()) :: {:ok, map()} | {:error, map()}
  def run(run_id, opts \\ []) when is_binary(run_id) and is_list(opts) do
    project_id = Keyword.get(opts, :project_id) || "default"
    input = Keyword.get(opts, :input) || %{}
    trigger = Keyword.get(opts, :trigger, "compensation")

    entries = SagaLog.list_actionable_reverse(run_id)

    log("compensation starting", %{
      run_id: run_id,
      count: length(entries),
      trigger: trigger
    })

    results =
      Enum.map(entries, fn entry ->
        execute_entry(entry, project_id, input)
      end)

    saga = SagaLog.list_for_run(run_id)
    any_failed? = Enum.any?(saga, &(&1.status == "failed"))
    rolled_back? = Enum.any?(saga, &(&1.status == "compensated"))

    if rolled_back? do
      Metrics.inc_rollback(if(any_failed?, do: "partial", else: "ok"))
    end

    {:ok, report} =
      Report.store(run_id, input,
        rolled_back: rolled_back?,
        saga: saga,
        trigger: trigger,
        project_id: project_id,
        error: if(any_failed?, do: "one or more compensators failed")
      )

    report =
      report
      |> Map.put("compensation_results", results)
      |> Map.put("complete", not any_failed?)

    if any_failed? do
      log("compensation finished with failures", %{run_id: run_id})
      {:error, report}
    else
      log("compensation finished", %{run_id: run_id, rolled_back: rolled_back?})
      {:ok, report}
    end
  end

  @doc """
  Execute a single compensator action (also used as forward task actions).
  """
  @spec execute_action(String.t(), map(), String.t(), map()) ::
          {:ok, map()} | {:error, String.t()}
  def execute_action(action, args, project_id, run_input)
      when is_binary(action) and is_map(args) and is_binary(project_id) and is_map(run_input) do
    deployment_id = deployment_id(args, run_input)

    case action do
      "control.rollback_deployment" ->
        with :ok <- require_deployment(deployment_id),
             {:ok, result} <- ControlClient.rollback_deployment(deployment_id, project_id, args) do
          Metrics.inc_compensation("ok")
          {:ok, result}
        else
          {:error, reason} ->
            Metrics.inc_compensation("failed")
            {:error, format_error(reason)}
        end

      "control.apply" ->
        with :ok <- require_deployment(deployment_id),
             {:ok, result} <- ControlClient.apply_change(deployment_id, project_id, args) do
          {:ok, result}
        else
          {:error, reason} -> {:error, format_error(reason)}
        end

      "report.store" ->
        Report.store(Map.get(args, "run_id") || "unknown", run_input,
          rolled_back: Map.get(args, "rolled_back", false),
          project_id: project_id,
          trigger: "report.store"
        )

      "noop" ->
        {:ok, %{"ok" => true, "action" => "noop"}}

      "fail.compensate" ->
        Metrics.inc_compensation("failed")
        {:error, "compensator failed"}

      other ->
        if String.starts_with?(other, "noop.") do
          Metrics.inc_compensation("ok")
          {:ok, %{"ok" => true, "action" => other, "compensated" => true}}
        else
          Metrics.inc_compensation("failed")
          {:error, "unsupported compensator: #{other}"}
        end
    end
  end

  defp execute_entry(entry, project_id, input) do
    case SagaLog.claim(entry.id) do
      {:error, :already_done} ->
        %{"step_id" => entry.step_id, "status" => "compensated", "skipped" => true}

      {:error, :not_found} ->
        %{"step_id" => entry.step_id, "status" => "missing"}

      {:ok, claimed} ->
        args = claimed.args || %{}

        case execute_action(claimed.compensator, args, project_id, input) do
          {:ok, result} ->
            _ = SagaLog.mark_compensated(claimed.id, result)

            log("compensator ok", %{
              run_id: claimed.run_id,
              step_id: claimed.step_id,
              compensator: claimed.compensator
            })

            %{
              "step_id" => claimed.step_id,
              "compensator" => claimed.compensator,
              "status" => "compensated",
              "result" => result
            }

          {:error, reason} ->
            _ = SagaLog.mark_failed(claimed.id, reason)

            log("compensator failed", %{
              run_id: claimed.run_id,
              step_id: claimed.step_id,
              compensator: claimed.compensator,
              error: reason
            })

            %{
              "step_id" => claimed.step_id,
              "compensator" => claimed.compensator,
              "status" => "failed",
              "error" => reason
            }
        end
    end
  end

  defp deployment_id(args, run_input) do
    Map.get(args, "deployment_id") ||
      Map.get(args, "deployment") ||
      Map.get(run_input, "deployment_id") ||
      Map.get(run_input, "deployment") ||
      get_in(run_input, ["event", "deployment_id"]) ||
      get_in(run_input, ["event", "deployment"])
  end

  defp require_deployment(id) when is_binary(id) and id != "", do: :ok
  defp require_deployment(_), do: {:error, "deployment_id required for control action"}

  defp format_error(reason) when is_binary(reason), do: reason
  defp format_error(reason), do: inspect(reason)

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
