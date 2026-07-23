defmodule DemoEventConsumer.Router do
  @moduledoc false

  use Plug.Router

  plug :match
  plug :dispatch

  get "/health/live" do
    send_json(conn, 200, %{status: "ok"})
  end

  get "/health/ready" do
    status = safe_status()

    if status[:ready] do
      send_json(conn, 200, %{status: "ok"})
    else
      send_json(conn, 503, %{status: "not_ready", detail: status})
    end
  end

  get "/" do
    cfg = Application.fetch_env!(:demo_event_consumer, :runtime_config)
    started_at = Application.fetch_env!(:demo_event_consumer, :started_at)
    uptime = System.monotonic_time(:second) - started_at

    send_json(conn, 200, %{
      service: cfg.service_name,
      language: "elixir",
      status: "running",
      version: cfg.service_version,
      uptime_seconds: uptime
    })
  end

  get "/v1/status" do
    send_json(conn, 200, safe_status())
  end

  match _ do
    send_json(conn, 404, %{error: "not_found"})
  end

  defp safe_status do
    try do
      DemoEventConsumer.Worker.status()
    catch
      :exit, _ -> %{ready: false, processed_count: 0, processed_ids: [], poison_naks: 0}
    end
  end

  defp send_json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body) <> "\n")
  end
end
