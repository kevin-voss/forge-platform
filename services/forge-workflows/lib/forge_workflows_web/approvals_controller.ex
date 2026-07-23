defmodule ForgeWorkflowsWeb.ApprovalsController do
  @moduledoc false

  import Plug.Conn

  alias ForgeWorkflows.Approvals.Store
  alias ForgeWorkflows.Engine.RunServer
  alias ForgeWorkflows.Runs

  @actor_header "x-forge-actor"

  @spec list_project(Plug.Conn.t()) :: Plug.Conn.t()
  def list_project(conn) do
    with {:ok, project_id} <- project_id(conn) do
      approvals = Enum.map(Store.list_for_project(project_id), &Store.to_api/1)
      send_json(conn, 200, %{"approvals" => approvals})
    else
      {:error, :project_required} -> project_required(conn)
    end
  end

  @spec list_for_run(Plug.Conn.t(), String.t()) :: Plug.Conn.t()
  def list_for_run(conn, run_id) do
    with {:ok, project_id} <- project_id(conn) do
      case Runs.get_run(run_id, project_id) do
        nil ->
          send_json(conn, 404, %{"error" => "run not found: #{run_id}", "code" => "run_not_found"})

        _run ->
          approvals = Enum.map(Store.list_for_run(run_id, project_id), &Store.to_api/1)
          send_json(conn, 200, %{"approvals" => approvals})
      end
    else
      {:error, :project_required} -> project_required(conn)
    end
  end

  @spec get(Plug.Conn.t(), String.t()) :: Plug.Conn.t()
  def get(conn, approval_id) do
    with {:ok, project_id} <- project_id(conn) do
      case Store.get(approval_id, project_id) do
        nil ->
          send_json(conn, 404, %{
            "error" => "approval not found: #{approval_id}",
            "code" => "approval_not_found"
          })

        approval ->
          send_json(conn, 200, Store.to_api(approval))
      end
    else
      {:error, :project_required} -> project_required(conn)
    end
  end

  @spec approve(Plug.Conn.t(), String.t()) :: Plug.Conn.t()
  def approve(conn, approval_id) do
    decide(conn, approval_id, "approved")
  end

  @spec deny(Plug.Conn.t(), String.t()) :: Plug.Conn.t()
  def deny(conn, approval_id) do
    decide(conn, approval_id, "denied")
  end

  defp decide(conn, approval_id, status) do
    with {:ok, project_id} <- project_id(conn) do
      actor = actor(conn)
      reason = read_reason(conn)

      case Store.decide(approval_id, project_id, status, actor, reason) do
        {:ok, approval} ->
          _ = RunServer.wake(approval.run_id, approval.step_id)
          send_json(conn, 200, Store.to_api(approval))

        {:error, :not_found} ->
          send_json(conn, 404, %{
            "error" => "approval not found: #{approval_id}",
            "code" => "approval_not_found"
          })

        {:error, :terminal} ->
          send_json(conn, 409, %{
            "error" => "approval already decided",
            "code" => "approval_terminal"
          })

        {:error, reason} ->
          send_json(conn, 500, %{
            "error" => "failed to decide approval: #{inspect(reason)}",
            "code" => "approval_decide_failed"
          })
      end
    else
      {:error, :project_required} -> project_required(conn)
    end
  end

  defp project_id(conn) do
    case get_req_header(conn, "x-forge-project") do
      [value | _] ->
        trimmed = String.trim(value)

        if trimmed == "" do
          {:error, :project_required}
        else
          {:ok, trimmed}
        end

      _ ->
        {:error, :project_required}
    end
  end

  defp actor(conn) do
    case get_req_header(conn, @actor_header) do
      [value | _] ->
        trimmed = String.trim(value)
        if trimmed == "", do: "anonymous", else: trimmed

      _ ->
        "anonymous"
    end
  end

  defp read_reason(conn) do
    case conn.body_params do
      %Plug.Conn.Unfetched{} ->
        nil

      params when is_map(params) ->
        case Map.get(params, "reason") do
          reason when is_binary(reason) -> String.trim(reason)
          _ -> nil
        end

      _ ->
        nil
    end
  end

  defp project_required(conn) do
    send_json(conn, 400, %{
      "error" => "X-Forge-Project header is required",
      "code" => "project_required"
    })
  end

  defp send_json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body) <> "\n")
  end
end
