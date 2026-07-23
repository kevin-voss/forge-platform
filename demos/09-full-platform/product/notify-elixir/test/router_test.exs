defmodule NotifyElixir.RouterTest do
  use ExUnit.Case, async: false
  import Plug.Test
  import Plug.Conn

  alias NotifyElixir.Config
  alias NotifyElixir.Router

  setup do
    cfg = %Config{
      port: 8080,
      service_name: "incident-notify",
      service_version: "0.1.0",
      log_level: "error",
      env: "test"
    }

    Application.put_env(:notify_elixir, :runtime_config, cfg)
    Application.put_env(:notify_elixir, :started_at, System.monotonic_time(:second) - 2)

    on_exit(fn ->
      Application.delete_env(:notify_elixir, :runtime_config)
      Application.delete_env(:notify_elixir, :started_at)
    end)

    :ok
  end

  test "health live and ready" do
    for path <- ["/health/live", "/health/ready"] do
      conn = conn(:get, path) |> Router.call([])
      assert conn.status == 200
      assert get_resp_header(conn, "content-type") |> hd() =~ "application/json"
      assert Jason.decode!(conn.resp_body) == %{"status" => "ok"}
    end
  end

  test "identity" do
    conn = conn(:get, "/") |> Router.call([])
    assert conn.status == 200
    body = Jason.decode!(conn.resp_body)
    assert body["service"] == "incident-notify"
    assert body["language"] == "elixir"
    assert body["status"] == "running"
    assert body["version"] == "0.1.0"
    assert is_number(body["uptime_seconds"])
    assert body["uptime_seconds"] >= 2
  end

  test "notify and list" do
    conn =
      conn(:post, "/notify", ~s({"channel":"ops","message":"incident opened"}))
      |> put_req_header("content-type", "application/json")
      |> Router.call([])

    assert conn.status == 202
    body = Jason.decode!(conn.resp_body)
    assert body["channel"] == "ops"
    assert body["status"] == "queued"

    list_conn = conn(:get, "/notifications") |> Router.call([])
    assert list_conn.status == 200
    items = Jason.decode!(list_conn.resp_body)["items"]
    assert Enum.any?(items, &(&1["message"] == "incident opened"))
  end
end
