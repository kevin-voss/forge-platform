defmodule NotifyElixir.Router do
  @moduledoc false

  use Plug.Router

  plug :match
  plug :dispatch

  get "/health/live" do
    send_json(conn, 200, %{status: "ok"})
  end

  get "/health/ready" do
    send_json(conn, 200, %{status: "ok"})
  end

  get "/" do
    cfg = Application.fetch_env!(:notify_elixir, :runtime_config)
    started_at = Application.fetch_env!(:notify_elixir, :started_at)
    uptime = System.monotonic_time(:second) - started_at

    send_json(conn, 200, %{
      service: cfg.service_name,
      language: "elixir",
      status: "running",
      version: cfg.service_version,
      uptime_seconds: uptime,
      notify: "POST /notify (stub until 54.04/54.05)"
    })
  end

  post "/notify" do
    {:ok, body, conn} = Plug.Conn.read_body(conn)

    case Jason.decode(body) do
      {:ok, payload} when is_map(payload) ->
        order_id = Map.get(payload, "orderId") || Map.get(payload, "order_id")
        channel = Map.get(payload, "channel") || "email"
        message = Map.get(payload, "message") || ""

        if is_binary(order_id) and order_id != "" do
          note = %{
            "id" => Base.encode16(:crypto.strong_rand_bytes(8), case: :lower),
            "orderId" => order_id,
            "channel" => channel,
            "message" => message,
            "status" => "queued"
          }

          NotifyElixir.Store.put(note)
          cfg = Application.fetch_env!(:notify_elixir, :runtime_config)
          NotifyElixir.JsonLog.info(cfg.service_name, "notification queued", %{orderId: order_id})
          send_json(conn, 202, note)
        else
          send_json(conn, 400, %{error: "orderId_required"})
        end

      {:error, _} ->
        send_json(conn, 400, %{error: "invalid_json"})
    end
  end

  get "/notifications" do
    send_json(conn, 200, %{items: NotifyElixir.Store.list()})
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
