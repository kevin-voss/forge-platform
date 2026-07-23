defmodule ForgeWorkflowsWeb.Router do
  @moduledoc false

  use Plug.Router

  alias ForgeWorkflows.Health

  plug ForgeWorkflowsWeb.RequestId
  plug :match
  plug :dispatch

  get "/health/live" do
    send_json(conn, 200, Health.live())
  end

  get "/health/ready" do
    body = Health.ready()
    send_json(conn, Health.ready_status_code(), body)
  end

  get "/" do
    cfg = Application.fetch_env!(:forge_workflows, :runtime_config)
    started_at = Application.fetch_env!(:forge_workflows, :started_at)
    uptime = System.monotonic_time(:second) - started_at

    send_json(conn, 200, %{
      service: cfg.service_name,
      language: "elixir",
      status: "running",
      version: cfg.service_version,
      uptime_seconds: uptime
    })
  end

  match _ do
    send_json(conn, 404, %{error: "not_found"})
  end

  defp send_json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body) <> "\n")
  end
end
