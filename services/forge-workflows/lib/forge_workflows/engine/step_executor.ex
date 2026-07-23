defmodule ForgeWorkflows.Engine.StepExecutor do
  @moduledoc false

  alias ForgeWorkflows.JsonLog

  @spec execute(map(), map()) :: {:ok, map()} | {:error, String.t()}
  def execute(step_def, run_input) when is_map(step_def) and is_map(run_input) do
    delay_ms = Map.get(step_def, :delay_ms) || Map.get(step_def, "delay_ms") || 0

    if is_integer(delay_ms) and delay_ms > 0 do
      Process.sleep(delay_ms)
    end

    case step_def[:type] || step_def["type"] do
      "noop" ->
        {:ok, %{"ok" => true, "action" => step_def[:action] || step_def["action"] || "noop"}}

      "log" ->
        message = step_def[:message] || step_def["message"] || "log step"
        service = service_name()

        JsonLog.info(service, "workflow step log", %{
          step_id: step_def[:id] || step_def["id"],
          message: message
        })

        {:ok, %{"logged" => message}}

      other ->
        {:error, "unsupported step type: #{inspect(other)}"}
    end
  end

  defp service_name do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{service_name: name} -> name
      _ -> "forge-workflows"
    end
  end
end
