defmodule ForgeWorkflowsWeb.RouterTest do
  use ExUnit.Case, async: false
  import Plug.Test
  import Plug.Conn

  alias ForgeWorkflows.Config
  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflowsWeb.Router

  setup do
    cfg = %Config{
      port: 8080,
      service_name: "forge-workflows",
      service_version: "0.1.0",
      log_level: "error",
      env: "test",
      shutdown_grace_ms: 10_000,
      database_url: "postgres://forge:forge@localhost:5432/forge_workflows",
      defs_dir: Path.expand("../definitions", __DIR__),
      max_parallelism: 8,
      default_step_timeout_ms: 300_000,
      scheduler_tick_ms: 1_000
    }

    Application.put_env(:forge_workflows, :runtime_config, cfg)
    Application.put_env(:forge_workflows, :started_at, System.monotonic_time(:second) - 2)

    if File.dir?(cfg.defs_dir) do
      Loader.put_definitions(Loader.load_dir!(cfg.defs_dir))
    end

    on_exit(fn ->
      Application.delete_env(:forge_workflows, :runtime_config)
      Application.delete_env(:forge_workflows, :started_at)
    end)

    :ok
  end

  test "health live and ready" do
    conn = conn(:get, "/health/live") |> Router.call([])
    assert conn.status == 200
    assert get_resp_header(conn, "content-type") |> hd() =~ "application/json"
    assert Jason.decode!(conn.resp_body) == %{"status" => "live"}
    assert get_resp_header(conn, "x-request-id") != []

    conn = conn(:get, "/health/ready") |> Router.call([])
    assert conn.status == 200
    assert Jason.decode!(conn.resp_body) == %{"status" => "ready"}
  end

  test "identity JSON fields present" do
    conn = conn(:get, "/") |> Router.call([])
    assert conn.status == 200
    body = Jason.decode!(conn.resp_body)
    assert body["service"] == "forge-workflows"
    assert body["language"] == "elixir"
    assert body["status"] == "running"
    assert body["version"] == "0.1.0"
    assert is_number(body["uptime_seconds"])
    assert body["uptime_seconds"] >= 2
  end

  test "lists workflows" do
    conn = conn(:get, "/v1/workflows") |> Router.call([])
    assert conn.status == 200
    body = Jason.decode!(conn.resp_body)
    names = Enum.map(body["workflows"], & &1["name"])
    assert "fixture-log" in names
  end

  test "run endpoints require project header" do
    conn = conn(:post, "/v1/workflows/fixture-log/runs", Jason.encode!(%{input: %{}}))
           |> put_req_header("content-type", "application/json")
           |> Router.call([])

    assert conn.status == 400
    assert Jason.decode!(conn.resp_body)["code"] == "project_required"
  end

  test "echoes X-Request-ID" do
    conn =
      conn(:get, "/health/live")
      |> put_req_header("x-request-id", "test-req-1")
      |> Router.call([])

    assert get_resp_header(conn, "x-request-id") == ["test-req-1"]
  end

  test "unknown path returns 404 JSON" do
    conn = conn(:get, "/nope") |> Router.call([])
    assert conn.status == 404
    assert Jason.decode!(conn.resp_body) == %{"error" => "not_found"}
  end
end
