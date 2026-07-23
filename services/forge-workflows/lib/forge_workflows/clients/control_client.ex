defmodule ForgeWorkflows.Clients.ControlClient do
  @moduledoc false

  @callback apply_change(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  @callback rollback_deployment(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  @callback get_reconcile(String.t(), String.t()) :: {:ok, map()} | {:error, term()}

  @spec apply_change(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  def apply_change(deployment_id, project_id, args \\ %{})
      when is_binary(deployment_id) and is_binary(project_id) and is_map(args) do
    impl().apply_change(deployment_id, project_id, args)
  end

  @spec rollback_deployment(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  def rollback_deployment(deployment_id, project_id, args \\ %{})
      when is_binary(deployment_id) and is_binary(project_id) and is_map(args) do
    impl().rollback_deployment(deployment_id, project_id, args)
  end

  @spec get_reconcile(String.t(), String.t()) :: {:ok, map()} | {:error, term()}
  def get_reconcile(deployment_id, project_id)
      when is_binary(deployment_id) and is_binary(project_id) do
    impl().get_reconcile(deployment_id, project_id)
  end

  defp impl do
    Application.get_env(
      :forge_workflows,
      :control_client,
      ForgeWorkflows.Clients.ControlClient.Default
    )
  end
end

defmodule ForgeWorkflows.Clients.ControlClient.Default do
  @moduledoc false

  @behaviour ForgeWorkflows.Clients.ControlClient

  alias ForgeWorkflows.Clients.ControlClient.Fake
  alias ForgeWorkflows.Clients.ControlClient.HTTP

  @impl true
  def apply_change(deployment_id, project_id, args) do
    case mode() do
      "fake" -> Fake.apply_change(deployment_id, project_id, args)
      "fail" -> {:error, "control unavailable"}
      _ -> HTTP.apply_change(deployment_id, project_id, args)
    end
  end

  @impl true
  def rollback_deployment(deployment_id, project_id, args) do
    case mode() do
      "fake" -> Fake.rollback_deployment(deployment_id, project_id, args)
      "fail" -> {:error, "control unavailable"}
      _ -> HTTP.rollback_deployment(deployment_id, project_id, args)
    end
  end

  @impl true
  def get_reconcile(deployment_id, project_id) do
    case mode() do
      "fake" -> Fake.get_reconcile(deployment_id, project_id)
      "fail" -> {:error, "control unavailable"}
      _ -> HTTP.get_reconcile(deployment_id, project_id)
    end
  end

  defp mode do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{control_mode: mode} when is_binary(mode) -> mode
      _ -> "fake"
    end
  end
end

defmodule ForgeWorkflows.Clients.ControlClient.HTTP do
  @moduledoc false

  alias ForgeWorkflows.Clients.Http

  @doc """
  Applies a desired-state change via Control `PATCH /v1/deployments/{id}`.
  """
  @spec apply_change(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  def apply_change(deployment_id, project_id, args) do
    body =
      args
      |> Map.take(["image", "desiredReplicas", "desired_replicas"])
      |> normalize_patch_body()
      |> then(fn
        map when map_size(map) == 0 -> %{"image" => Map.get(args, "image") || "forge/applied:latest"}
        map -> map
      end)
      |> Jason.encode!()

    path = "/v1/deployments/#{URI.encode(deployment_id)}"

    case Http.request(:patch, url(path), headers(project_id), body, timeout_ms()) do
      {:ok, status, resp} when status in [200, 202] ->
        with {:ok, map} <- decode_map(resp) do
          {:ok,
           %{
             "deployment_id" => deployment_id,
             "action" => "control.apply",
             "deployment" => map
           }}
        end

      {:ok, status, resp} ->
        {:error, {:http, status, resp}}

      {:error, reason} ->
        {:error, reason}
    end
  end

  @doc """
  Rolls a deployment back to Control's `lastHealthyImage` via documented APIs:
  `GET /v1/deployments/{id}/reconcile` then `PATCH /v1/deployments/{id}` with that image.
  """
  @spec rollback_deployment(String.t(), String.t(), map()) :: {:ok, map()} | {:error, term()}
  def rollback_deployment(deployment_id, project_id, _args) do
    with {:ok, reconcile} <- get_reconcile(deployment_id, project_id),
         {:ok, image} <- last_healthy_image(reconcile) do
      body = Jason.encode!(%{"image" => image})
      path = "/v1/deployments/#{URI.encode(deployment_id)}"

      case Http.request(:patch, url(path), headers(project_id), body, timeout_ms()) do
        {:ok, status, resp} when status in [200, 202] ->
          with {:ok, map} <- decode_map(resp) do
            {:ok,
             %{
               "deployment_id" => deployment_id,
               "action" => "control.rollback_deployment",
               "restored_image" => image,
               "deployment" => map,
               "reconcile_status" => Map.get(reconcile, "status")
             }}
          end

        {:ok, status, resp} ->
          {:error, {:http, status, resp}}

        {:error, reason} ->
          {:error, reason}
      end
    end
  end

  @spec get_reconcile(String.t(), String.t()) :: {:ok, map()} | {:error, term()}
  def get_reconcile(deployment_id, project_id) do
    path = "/v1/deployments/#{URI.encode(deployment_id)}/reconcile"

    case Http.request(:get, url(path), headers(project_id), nil, timeout_ms()) do
      {:ok, 200, resp} ->
        decode_map(resp)

      {:ok, status, resp} ->
        {:error, {:http, status, resp}}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp last_healthy_image(reconcile) when is_map(reconcile) do
    image =
      Map.get(reconcile, "lastHealthyImage") ||
        Map.get(reconcile, "last_healthy_image")

    if is_binary(image) and image != "" do
      {:ok, image}
    else
      {:error, "no lastHealthyImage on reconcile status"}
    end
  end

  defp normalize_patch_body(map) do
    Map.new(map, fn
      {"desired_replicas", v} -> {"desiredReplicas", v}
      {k, v} -> {k, v}
    end)
    |> Map.reject(fn {_k, v} -> is_nil(v) end)
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
        %{control_url: url} when is_binary(url) -> String.trim_trailing(url, "/")
        _ -> "http://forge-control:4001"
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
      %{control_http_timeout_ms: ms} when is_integer(ms) -> ms
      _ -> 10_000
    end
  end
end

defmodule ForgeWorkflows.Clients.ControlClient.Fake do
  @moduledoc false

  @table :forge_workflows_fake_control

  defp ensure_table! do
    case :ets.whereis(@table) do
      :undefined ->
        :ets.new(@table, [:named_table, :public, :set, write_concurrency: true])

      _ ->
        @table
    end

    :ok
  end

  @spec reset!() :: :ok
  def reset! do
    ensure_table!()
    :ets.delete_all_objects(@table)
    :ok
  end

  @spec calls() :: [map()]
  def calls do
    ensure_table!()

    case :ets.lookup(@table, :calls) do
      [{:calls, list}] -> Enum.reverse(list)
      [] -> []
    end
  end

  @spec rollback_count(String.t()) :: non_neg_integer()
  def rollback_count(deployment_id) when is_binary(deployment_id) do
    calls()
    |> Enum.count(fn c ->
      c["action"] == "control.rollback_deployment" and c["deployment_id"] == deployment_id
    end)
  end

  @spec apply_change(String.t(), String.t(), map()) :: {:ok, map()}
  def apply_change(deployment_id, project_id, args) do
    ensure_table!()

    result = %{
      "deployment_id" => deployment_id,
      "project_id" => project_id,
      "action" => "control.apply",
      "image" => Map.get(args, "image") || "forge/applied:latest",
      "applied" => true
    }

    push_call(result)
    put_state(deployment_id, %{"image" => result["image"], "lastHealthyImage" => "forge/healthy:v1"})
    {:ok, result}
  end

  @spec rollback_deployment(String.t(), String.t(), map()) :: {:ok, map()}
  def rollback_deployment(deployment_id, project_id, _args) do
    ensure_table!()
    state = get_state(deployment_id)
    image = Map.get(state, "lastHealthyImage") || "forge/healthy:v1"

    result = %{
      "deployment_id" => deployment_id,
      "project_id" => project_id,
      "action" => "control.rollback_deployment",
      "restored_image" => image,
      "status" => "rolled_back"
    }

    push_call(result)
    put_state(deployment_id, Map.put(state, "image", image))
    {:ok, result}
  end

  @spec get_reconcile(String.t(), String.t()) :: {:ok, map()}
  def get_reconcile(deployment_id, _project_id) do
    ensure_table!()
    state = get_state(deployment_id)

    {:ok,
     %{
       "deploymentId" => deployment_id,
       "status" => "deployed",
       "lastHealthyImage" => Map.get(state, "lastHealthyImage") || "forge/healthy:v1",
       "currentImage" => Map.get(state, "image") || "forge/applied:latest",
       "controllerHealthy" => true
     }}
  end

  defp push_call(call) do
    ensure_table!()

    list =
      case :ets.lookup(@table, :calls) do
        [{:calls, existing}] -> [call | existing]
        [] -> [call]
      end

    :ets.insert(@table, {:calls, list})
  end

  defp get_state(deployment_id) do
    case :ets.lookup(@table, {:dep, deployment_id}) do
      [{{:dep, ^deployment_id}, state}] -> state
      [] -> %{}
    end
  end

  defp put_state(deployment_id, state) do
    :ets.insert(@table, {{:dep, deployment_id}, state})
  end
end
