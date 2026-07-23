defmodule ForgeWorkflows.Steps.Approval do
  @moduledoc false

  alias ForgeWorkflows.Approvals.Store
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Runs
  alias ForgeWorkflows.Steps.Agent

  @spec park(String.t(), map(), String.t(), map(), pos_integer()) ::
          {:ok, ForgeWorkflows.Schemas.Approval.t()} | {:error, term()}
  def park(run_id, step_def, project_id, run_input, ttl_seconds)
      when is_binary(run_id) and is_map(step_def) and is_binary(project_id) and is_map(run_input) and
             is_integer(ttl_seconds) and ttl_seconds >= 1 do
    step_id = step_def[:id] || step_def["id"]
    prompt_tmpl = step_def[:prompt] || step_def["prompt"] || "Approval required"
    prompt = resolve_prompt(prompt_tmpl, run_input)

    with {:ok, approval} <- Store.create(run_id, step_id, project_id, prompt, ttl_seconds),
         {:ok, _} <- Runs.mark_step_waiting(run_id, step_id, approval.expires_at),
         {:ok, _} <- Runs.mark_run_awaiting_approval(run_id, step_id) do
      log("approval step parked", %{
        run_id: run_id,
        step_id: step_id,
        approval_id: approval.id,
        expires_at: approval.expires_at && DateTime.to_iso8601(approval.expires_at)
      })

      {:ok, approval}
    end
  end

  @spec resolve_prompt(String.t(), map()) :: String.t()
  def resolve_prompt(prompt, context) when is_binary(prompt) and is_map(context) do
    Regex.replace(~r/\$\{([a-zA-Z0-9_.]+)\}/, prompt, fn full, path ->
      case Agent.resolve_value("${#{path}}", context) do
        "${" <> _ -> full
        value -> to_string(value)
      end
    end)
  end

  def resolve_prompt(prompt, _context), do: to_string(prompt)

  @spec apply_decision(String.t(), map(), ForgeWorkflows.Schemas.Approval.t(), [map()]) ::
          {:cont, :continue} | {:halt, :waiting} | {:halt, {:error, term()}}
  def apply_decision(run_id, step_def, approval, workflow_steps)
      when is_binary(run_id) and is_map(step_def) and is_list(workflow_steps) do
    step_id = step_def[:id] || step_def["id"]

    case approval.status do
      "pending" ->
        {:halt, :waiting}

      "approved" ->
        output = decision_output(approval)

        case Runs.complete_step(run_id, step_id, output) do
          {:ok, _} ->
            _ = skip_on_deny_target(run_id, step_def)
            _ = Runs.mark_run_running(run_id, step_id)
            log("approval approved; resuming", %{run_id: run_id, step_id: step_id})
            {:cont, :continue}

          {:error, reason} ->
            {:halt, {:error, reason}}
        end

      status when status in ["denied", "expired"] ->
        output = decision_output(approval)

        case Runs.complete_step(run_id, step_id, output) do
          {:ok, _} ->
            apply_deny_path(run_id, step_def, workflow_steps, status)

          {:error, reason} ->
            {:halt, {:error, reason}}
        end

      other ->
        {:halt, {:error, "unexpected approval status: #{inspect(other)}"}}
    end
  end

  defp apply_deny_path(run_id, step_def, workflow_steps, status) do
    step_id = step_def[:id] || step_def["id"]
    on_deny = step_def[:on_deny] || step_def["on_deny"]

    case on_deny do
      target when is_binary(target) and target != "" ->
        case skip_until(run_id, workflow_steps, step_id, target) do
          :ok ->
            _ = Runs.mark_run_running(run_id, target)
            log("approval #{status}; following on_deny", %{
              run_id: run_id,
              step_id: step_id,
              on_deny: target
            })

            {:cont, :continue}

          {:error, reason} ->
            _ = Runs.mark_run_failed(run_id, reason)
            {:halt, {:error, reason}}
        end

      _ ->
        reason = "approval #{status}"
        _ = Runs.mark_run_failed(run_id, reason)
        log("approval #{status}; failing run", %{run_id: run_id, step_id: step_id})
        {:halt, {:error, reason}}
    end
  end

  defp skip_until(run_id, workflow_steps, approval_step_id, target_id) do
    ids = Enum.map(workflow_steps, & &1.id)

    unless target_id in ids do
      {:error, "on_deny step not found: #{target_id}"}
    else
      approval_idx = Enum.find_index(workflow_steps, &(&1.id == approval_step_id)) || -1

      workflow_steps
      |> Enum.drop(approval_idx + 1)
      |> Enum.reduce_while(:ok, fn step, :ok ->
        if step.id == target_id do
          {:halt, :ok}
        else
          _ =
            Runs.skip_step(run_id, step.id, %{
              "skipped" => true,
              "reason" => "approval_denied"
            })

          {:cont, :ok}
        end
      end)
      |> case do
        :ok -> :ok
        other -> other
      end
    end
  end

  defp skip_on_deny_target(run_id, step_def) do
    case step_def[:on_deny] || step_def["on_deny"] do
      target when is_binary(target) and target != "" ->
        Runs.skip_step(run_id, target, %{"skipped" => true, "reason" => "approval_approved"})

      _ ->
        :ok
    end
  end

  defp decision_output(approval) do
    %{
      "decision" => approval.status,
      "approval_id" => approval.id,
      "prompt" => approval.prompt
    }
    |> maybe_put("decided_by", approval.decided_by)
    |> maybe_put("reason", approval.reason)
  end

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
