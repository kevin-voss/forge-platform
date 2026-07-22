defmodule DemoElixirApi.RouterTest do
  use ExUnit.Case, async: false
  import Plug.Test
  import Plug.Conn

  alias DemoElixirApi.Config
  alias DemoElixirApi.Router

  setup do
    cfg = %Config{
      port: 8080,
      service_name: "demo-elixir-api",
      service_version: "0.1.0",
      log_level: "error",
      env: "test"
    }

    Application.put_env(:demo_elixir_api, :runtime_config, cfg)
    Application.put_env(:demo_elixir_api, :started_at, System.monotonic_time(:second) - 2)

    on_exit(fn ->
      Application.delete_env(:demo_elixir_api, :runtime_config)
      Application.delete_env(:demo_elixir_api, :started_at)
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
    assert body["service"] == "demo-elixir-api"
    assert body["language"] == "elixir"
    assert body["status"] == "running"
    assert body["version"] == "0.1.0"
    assert is_number(body["uptime_seconds"])
    assert body["uptime_seconds"] >= 2
  end

  test "unknown path returns 404 JSON" do
    conn = conn(:get, "/nope") |> Router.call([])
    assert conn.status == 404
    assert Jason.decode!(conn.resp_body) == %{"error" => "not_found"}
  end
end
