defmodule ForgeWorkflows.Steps.Report do
  @moduledoc false

  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Saga.Log, as: SagaLog

  @spec store(String.t(), map(), keyword()) :: {:ok, map()} | {:error, String.t()}
  def store(run_id, run_input, opts \\ [])
      when is_binary(run_id) and is_map(run_input) and is_list(opts) do
    rolled_back = Keyword.get(opts, :rolled_back, false)
    saga = Keyword.get(opts, :saga) || SagaLog.list_for_run(run_id)
    trigger = Keyword.get(opts, :trigger, "manual")
    error = Keyword.get(opts, :error)

    report = %{
      "run_id" => run_id,
      "rolled_back" => rolled_back == true,
      "trigger" => trigger,
      "deployment_id" => deployment_id(run_input),
      "saga" => SagaLog.to_api(saga),
      "generated_at" => DateTime.utc_now() |> DateTime.to_iso8601()
    }
    |> maybe_put("error", error)
    |> maybe_put("project_id", Keyword.get(opts, :project_id))

    ref = persist_optional_artifact(report)
    output = Map.put(report, "report_ref", ref)

    log("report stored", %{
      run_id: run_id,
      rolled_back: output["rolled_back"],
      report_ref: ref
    })

    {:ok, output}
  end

  @spec build_run_result(map(), boolean()) :: map()
  def build_run_result(report, ok?) when is_map(report) do
    %{
      "ok" => ok? == true,
      "rolled_back" => report["rolled_back"] == true,
      "report" => report,
      "report_ref" => report["report_ref"]
    }
  end

  defp persist_optional_artifact(report) do
    bucket =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{report_bucket: b} when is_binary(b) and b != "" -> b
        _ -> nil
      end

    case bucket do
      nil ->
        "inline://workflow-report/#{report["run_id"]}"

      bucket ->
        # Optional Storage publish is deferred; record a stable reference shape.
        "storage://#{bucket}/workflow-reports/#{report["run_id"]}.json"
    end
  end

  defp deployment_id(input) when is_map(input) do
    Map.get(input, "deployment_id") ||
      Map.get(input, "deployment") ||
      get_in(input, ["event", "deployment_id"]) ||
      get_in(input, ["event", "deployment"])
  end

  defp deployment_id(_), do: nil

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
