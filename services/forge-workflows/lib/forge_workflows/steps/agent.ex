defmodule ForgeWorkflows.Steps.Agent do
  @moduledoc false

  alias ForgeWorkflows.Clients.AgentClient
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics

  @terminal ~w(succeeded failed cancelled stopped)
  @awaiting "awaiting_approval"

  @spec execute(map(), map(), keyword()) :: {:ok, map()} | {:error, String.t()}
  def execute(step_def, run_input, opts \\ [])
      when is_map(step_def) and is_map(run_input) and is_list(opts) do
    agent = step_def[:agent] || step_def["agent"]
    project_id = Keyword.get(opts, :project_id) || project_from_input(run_input)
    poll_ms = Keyword.get(opts, :poll_ms) || agent_poll_ms()
    timeout_ms = Keyword.get(opts, :timeout_ms) || agent_step_timeout_ms()

    with :ok <- require_agent(agent),
         :ok <- require_project(project_id),
         {:ok, input} <- resolve_input(step_def[:input] || step_def["input"] || %{}, run_input),
         {:ok, start} <- AgentClient.start_run(agent, project_id, input, %{"dry_run" => true}),
         {:ok, agent_run_id} <- fetch_run_id(start),
         {:ok, final} <- poll(agent_run_id, project_id, poll_ms, timeout_ms, monotonic_now()) do
      output = map_result(agent, agent_run_id, final, input)
      Metrics.inc_agent_step(output["status"] || "succeeded")
      log("agent step completed", %{agent: agent, agent_run_id: agent_run_id, status: output["status"]})
      {:ok, output}
    else
      {:error, reason} when is_binary(reason) ->
        Metrics.inc_agent_step("failed")
        {:error, reason}

      {:error, reason} ->
        Metrics.inc_agent_step("failed")
        {:error, format_error(reason)}
    end
  end

  @spec resolve_input(term(), map()) :: {:ok, map()} | {:error, String.t()}
  def resolve_input(template, context) when is_map(template) and is_map(context) do
    resolved =
      Map.new(template, fn {k, v} ->
        key = if is_atom(k), do: Atom.to_string(k), else: to_string(k)
        {key, resolve_value(v, context)}
      end)

    {:ok, resolved}
  end

  def resolve_input(nil, _context), do: {:ok, %{}}
  def resolve_input(_, _), do: {:error, "agent input must be a map"}

  @spec resolve_value(term(), map()) :: term()
  def resolve_value(value, context) when is_binary(value) and is_map(context) do
    case Regex.run(~r/^\$\{([a-zA-Z0-9_.]+)\}$/, value) do
      [_, path] ->
        get_path(context, String.split(path, ".")) || value

      _ ->
        value
    end
  end

  def resolve_value(value, context) when is_map(value) and is_map(context) do
    Map.new(value, fn {k, v} ->
      key = if is_atom(k), do: Atom.to_string(k), else: to_string(k)
      {key, resolve_value(v, context)}
    end)
  end

  def resolve_value(value, context) when is_list(value) and is_map(context) do
    Enum.map(value, &resolve_value(&1, context))
  end

  def resolve_value(value, _context), do: value

  defp poll(run_id, project_id, poll_ms, timeout_ms, started_at) do
    if monotonic_now() - started_at > timeout_ms do
      {:error, "agent step timeout"}
    else
      case AgentClient.get_run(run_id, project_id) do
        {:ok, %{"status" => status} = run} when status in @terminal ->
          if status == "succeeded" do
            {:ok, run}
          else
            {:error, run["error"] || "agent run #{status}"}
          end

        {:ok, %{"status" => @awaiting} = run} ->
          # Surfaced for 16.04; durable workflow-level wait lands in 16.05.
          {:ok, run}

        {:ok, %{"status" => "running"}} ->
          Process.sleep(poll_ms)
          poll(run_id, project_id, poll_ms, timeout_ms, started_at)

        {:ok, %{"status" => other}} ->
          Process.sleep(poll_ms)

          if other in ["queued", "pending"] do
            poll(run_id, project_id, poll_ms, timeout_ms, started_at)
          else
            {:error, "unexpected agent status: #{other}"}
          end

        {:error, reason} ->
          {:error, reason}
      end
    end
  end

  defp map_result(agent, agent_run_id, final, input) do
    status = final["status"] || "succeeded"

    base = %{
      "agent" => agent,
      "agent_run_id" => agent_run_id,
      "status" => status,
      "input" => input
    }

    base =
      case final["result"] do
        nil -> base
        result -> Map.put(base, "result", result)
      end

    base =
      case final["pending_approval"] do
        nil -> base
        approval -> Map.put(base, "pending_approval", approval)
      end

    if status == @awaiting do
      Map.put(base, "awaiting_approval", true)
    else
      base
    end
  end

  defp fetch_run_id(%{"run_id" => id}) when is_binary(id) and id != "", do: {:ok, id}
  defp fetch_run_id(_), do: {:error, "agent start missing run_id"}

  defp require_agent(agent) when is_binary(agent) and agent != "", do: :ok
  defp require_agent(_), do: {:error, "agent step requires agent name"}

  defp require_project(project_id) when is_binary(project_id) and project_id != "", do: :ok
  defp require_project(_), do: {:error, "agent step requires project_id"}

  defp project_from_input(input) do
    Map.get(input, "project_id") || Map.get(input, :project_id)
  end

  defp get_path(map, []), do: map

  defp get_path(map, [head | rest]) when is_map(map) do
    case Map.get(map, head) || Map.get(map, String.to_atom(head)) do
      nil -> nil
      value -> get_path(value, rest)
    end
  end

  defp get_path(_, _), do: nil

  defp agent_poll_ms do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{agent_poll_ms: ms} when is_integer(ms) -> ms
      _ -> 1_000
    end
  end

  defp agent_step_timeout_ms do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{agent_step_timeout_ms: ms} when is_integer(ms) -> ms
      _ -> 300_000
    end
  end

  defp monotonic_now, do: System.monotonic_time(:millisecond)

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
