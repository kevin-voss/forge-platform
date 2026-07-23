defmodule ForgeWorkflows.Engine.StepExecutor do
  @moduledoc false

  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Steps.Timeout

  @spec execute(map(), map(), keyword()) :: {:ok, map()} | {:error, String.t()}
  def execute(step_def, run_input, opts \\ [])
      when is_map(step_def) and is_map(run_input) and is_list(opts) do
    attempt = Keyword.get(opts, :attempt, 1)
    default_timeout = Keyword.get(opts, :timeout_ms, 300_000)
    timeout_ms = Timeout.step_timeout_ms(step_def, default_timeout)

    # In-process delay_ms on log/noop/task remains for short sleeps; durable
    # waits use type: delay via the engine + scheduler.
    delay_ms = Map.get(step_def, :delay_ms) || Map.get(step_def, "delay_ms") || 0

    if is_integer(delay_ms) and delay_ms > 0 do
      type = step_def[:type] || step_def["type"]

      if type != "delay" do
        Process.sleep(delay_ms)
      end
    end

    Timeout.execute_with_timeout(fn -> do_execute(step_def, run_input, attempt) end, timeout_ms)
  end

  defp do_execute(step_def, run_input, attempt) do
    case step_def[:type] || step_def["type"] do
      "noop" ->
        {:ok, %{"ok" => true, "action" => step_def[:action] || step_def["action"] || "noop"}}

      "task" ->
        execute_action(step_def, run_input, attempt)

      "timeout" ->
        execute_action(step_def, run_input, attempt)

      "retry" ->
        execute_action(step_def, run_input, attempt)

      "log" ->
        message = step_def[:message] || step_def["message"] || "log step"
        service = service_name()

        JsonLog.info(service, "workflow step log", %{
          step_id: step_def[:id] || step_def["id"],
          message: message
        })

        {:ok, %{"logged" => message}}

      "delay" ->
        {:ok, %{"delayed" => true}}

      "parallel" ->
        {:error, "parallel must be executed by the engine"}

      "conditional" ->
        {:error, "conditional must be executed by the engine"}

      other ->
        {:error, "unsupported step type: #{inspect(other)}"}
    end
  end

  defp execute_action(step_def, _run_input, attempt) do
    action = step_def[:action] || step_def["action"] || "noop"

    case action do
      "noop" ->
        {:ok, %{"ok" => true, "action" => "noop", "attempt" => attempt}}

      "fail" ->
        {:error, "action failed"}

      "fail_until" ->
        succeed_on =
          step_def[:succeed_on_attempt] || step_def["succeed_on_attempt"] || 2

        if attempt >= succeed_on do
          {:ok, %{"ok" => true, "action" => "fail_until", "attempt" => attempt}}
        else
          {:error, "transient failure on attempt #{attempt}"}
        end

      "sleep" ->
        ms = step_def[:delay_ms] || step_def["delay_ms"] || 1_000
        Process.sleep(ms)
        {:ok, %{"ok" => true, "action" => "sleep", "slept_ms" => ms}}

      other ->
        {:error, "unsupported action: #{inspect(other)}"}
    end
  end

  defp service_name do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{service_name: name} -> name
      _ -> "forge-workflows"
    end
  end
end
