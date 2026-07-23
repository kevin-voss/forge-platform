defmodule ForgeWorkflowsWeb.RequestId do
  @moduledoc false

  @behaviour Plug

  import Plug.Conn

  @impl true
  def init(opts), do: opts

  @impl true
  def call(conn, _opts) do
    request_id =
      case get_req_header(conn, "x-request-id") do
        [value | _] ->
          trimmed = String.trim(value)
          if trimmed == "", do: mint_request_id(), else: trimmed

        _ ->
          mint_request_id()
      end

    conn
    |> assign(:request_id, request_id)
    |> put_resp_header("x-request-id", request_id)
    |> register_before_send(fn conn ->
      service =
        case Application.get_env(:forge_workflows, :runtime_config) do
          %{service_name: name} -> name
          _ -> "forge-workflows"
        end

      ForgeWorkflows.JsonLog.info(service, "request completed", %{
        request_id: request_id,
        method: conn.method,
        path: conn.request_path,
        status_code: conn.status
      })

      conn
    end)
  end

  defp mint_request_id do
    Base.encode16(:crypto.strong_rand_bytes(16), case: :lower)
  end
end
