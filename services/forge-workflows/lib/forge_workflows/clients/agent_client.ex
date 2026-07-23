defmodule ForgeWorkflows.Clients.AgentClient do
  @moduledoc false

  @callback start_run(String.t(), String.t(), term(), map()) ::
              {:ok, map()} | {:error, term()}
  @callback get_run(String.t(), String.t()) :: {:ok, map()} | {:error, term()}

  @spec start_run(String.t(), String.t(), term(), map()) :: {:ok, map()} | {:error, term()}
  def start_run(agent, project_id, input, context \\ %{})
      when is_binary(agent) and is_binary(project_id) and is_map(context) do
    impl().start_run(agent, project_id, input, context)
  end

  @spec get_run(String.t(), String.t()) :: {:ok, map()} | {:error, term()}
  def get_run(run_id, project_id) when is_binary(run_id) and is_binary(project_id) do
    impl().get_run(run_id, project_id)
  end

  defp impl do
    Application.get_env(
      :forge_workflows,
      :agent_client,
      ForgeWorkflows.Clients.AgentClient.Default
    )
  end
end

defmodule ForgeWorkflows.Clients.AgentClient.Default do
  @moduledoc false

  @behaviour ForgeWorkflows.Clients.AgentClient

  alias ForgeWorkflows.Clients.AgentClient.Fake
  alias ForgeWorkflows.Clients.AgentClient.HTTP

  @impl true
  def start_run(agent, project_id, input, context) do
    case mode() do
      "fake" -> Fake.start_run(agent, project_id, input, context)
      "fail" -> {:error, "agent unavailable"}
      "awaiting" -> Fake.start_run_awaiting(agent, project_id, input, context)
      _ -> HTTP.start_run(agent, project_id, input, context)
    end
  end

  @impl true
  def get_run(run_id, project_id) do
    case mode() do
      "fake" -> Fake.get_run(run_id, project_id)
      "fail" -> {:error, "agent unavailable"}
      "awaiting" -> Fake.get_run(run_id, project_id)
      _ -> HTTP.get_run(run_id, project_id)
    end
  end

  defp mode do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{agents_mode: mode} when is_binary(mode) -> mode
      _ -> "fake"
    end
  end
end

defmodule ForgeWorkflows.Clients.AgentClient.HTTP do
  @moduledoc false

  alias ForgeWorkflows.Clients.Http

  @spec start_run(String.t(), String.t(), term(), map()) :: {:ok, map()} | {:error, term()}
  def start_run(agent, project_id, input, context) do
    body =
      Jason.encode!(%{
        "input" => input,
        "context" => context
      })

    path = "/v1/agents/#{URI.encode(agent)}/runs"

    case Http.request(:post, url(path), headers(project_id), body, timeout_ms()) do
      {:ok, status, resp} when status in [200, 202] ->
        decode_map(resp)

      {:ok, status, resp} ->
        {:error, {:http, status, resp}}

      {:error, reason} ->
        {:error, reason}
    end
  end

  @spec get_run(String.t(), String.t()) :: {:ok, map()} | {:error, term()}
  def get_run(run_id, project_id) do
    path = "/v1/runs/#{URI.encode(run_id)}"

    case Http.request(:get, url(path), headers(project_id), nil, timeout_ms()) do
      {:ok, 200, resp} ->
        decode_map(resp)

      {:ok, status, resp} ->
        {:error, {:http, status, resp}}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp decode_map(resp) do
    case Jason.decode(resp) do
      {:ok, map} when is_map(map) -> {:ok, map}
      {:ok, _} -> {:error, :invalid_json}
      {:error, reason} -> {:error, reason}
    end
  end

  defp url(path) do
    base =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{agents_url: url} when is_binary(url) -> String.trim_trailing(url, "/")
        _ -> "http://forge-agents:4301"
      end

    base <> path
  end

  defp headers(project_id) do
    [
      {"content-type", "application/json"},
      {"accept", "application/json"},
      {"x-forge-project", project_id}
    ]
  end

  defp timeout_ms do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{agents_http_timeout_ms: ms} when is_integer(ms) -> ms
      _ -> 10_000
    end
  end
end

defmodule ForgeWorkflows.Clients.AgentClient.Fake do
  @moduledoc false

  @table :forge_workflows_fake_agent_runs

  defp ensure_table! do
    case :ets.whereis(@table) do
      :undefined ->
        :ets.new(@table, [:named_table, :public, :set, write_concurrency: true])

      _ ->
        @table
    end

    :ok
  end

  @spec start_run(String.t(), String.t(), term(), map()) :: {:ok, map()}
  def start_run(agent, project_id, input, _context) do
    ensure_table!()
    run_id = Ecto.UUID.generate()

    result = %{
      "run_id" => run_id,
      "project_id" => project_id,
      "agent" => agent,
      "status" => "succeeded",
      "step_count" => 1,
      "started_at" => DateTime.utc_now() |> DateTime.to_iso8601(),
      "ended_at" => DateTime.utc_now() |> DateTime.to_iso8601(),
      "result" => fake_result(agent, input)
    }

    :ets.insert(@table, {run_id, result})
    {:ok, %{"run_id" => run_id, "status" => "running"}}
  end

  @spec start_run_awaiting(String.t(), String.t(), term(), map()) :: {:ok, map()}
  def start_run_awaiting(agent, project_id, input, _context) do
    ensure_table!()
    run_id = Ecto.UUID.generate()

    result = %{
      "run_id" => run_id,
      "project_id" => project_id,
      "agent" => agent,
      "status" => "awaiting_approval",
      "step_count" => 1,
      "started_at" => DateTime.utc_now() |> DateTime.to_iso8601(),
      "pending_approval" => %{
        "id" => Ecto.UUID.generate(),
        "run_id" => run_id,
        "tool" => "runtime.restart",
        "args" => %{"deployment_id" => extract_deployment(input)},
        "status" => "pending",
        "created_at" => DateTime.utc_now() |> DateTime.to_iso8601(),
        "expires_at" =>
          DateTime.utc_now() |> DateTime.add(3600, :second) |> DateTime.to_iso8601()
      },
      "result" => fake_result(agent, input)
    }

    :ets.insert(@table, {run_id, result})
    {:ok, %{"run_id" => run_id, "status" => "running"}}
  end

  @spec get_run(String.t(), String.t()) :: {:ok, map()} | {:error, term()}
  def get_run(run_id, _project_id) do
    ensure_table!()

    case :ets.lookup(@table, run_id) do
      [{^run_id, result}] -> {:ok, result}
      [] -> {:error, :not_found}
    end
  end

  defp fake_result(agent, input) do
    deployment = extract_deployment(input)

    Jason.encode!(%{
      "agent" => agent,
      "diagnosis" => "synthetic diagnosis for #{deployment}",
      "deployment_id" => deployment
    })
  end

  defp extract_deployment(input) when is_map(input) do
    Map.get(input, "deployment") ||
      Map.get(input, "deployment_id") ||
      get_in(input, ["event", "deployment_id"]) ||
      "unknown"
  end

  defp extract_deployment(_), do: "unknown"
end
