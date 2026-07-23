defmodule ForgeWorkflows.ConfigTest do
  use ExUnit.Case, async: false

  alias ForgeWorkflows.Config

  setup do
    original = %{
      "PORT" => System.get_env("PORT"),
      "FORGE_SERVICE_NAME" => System.get_env("FORGE_SERVICE_NAME"),
      "FORGE_SERVICE_VERSION" => System.get_env("FORGE_SERVICE_VERSION"),
      "FORGE_LOG_LEVEL" => System.get_env("FORGE_LOG_LEVEL"),
      "FORGE_ENV" => System.get_env("FORGE_ENV"),
      "FORGE_SHUTDOWN_GRACE_SECONDS" => System.get_env("FORGE_SHUTDOWN_GRACE_SECONDS"),
      "FORGE_WORKFLOWS_DATABASE_URL" => System.get_env("FORGE_WORKFLOWS_DATABASE_URL"),
      "FORGE_WORKFLOWS_DEFS_DIR" => System.get_env("FORGE_WORKFLOWS_DEFS_DIR")
    }

    defs = Path.expand("../definitions", __DIR__)
    System.put_env("FORGE_WORKFLOWS_DEFS_DIR", defs)
    System.put_env("FORGE_WORKFLOWS_DATABASE_URL", "postgres://forge:forge@localhost:5432/forge_workflows")

    on_exit(fn ->
      Enum.each(original, fn {key, value} ->
        if value == nil do
          System.delete_env(key)
        else
          System.put_env(key, value)
        end
      end)
    end)

    :ok
  end

  test "requires PORT" do
    System.delete_env("PORT")
    System.put_env("FORGE_LOG_LEVEL", "info")

    assert_raise ArgumentError, ~r/PORT is required/, fn ->
      Config.load!()
    end
  end

  test "requires DATABASE_URL" do
    System.put_env("PORT", "8080")
    System.delete_env("FORGE_WORKFLOWS_DATABASE_URL")

    assert_raise ArgumentError, ~r/FORGE_WORKFLOWS_DATABASE_URL is required/, fn ->
      Config.load!()
    end
  end

  test "rejects invalid PORT" do
    System.put_env("PORT", "not-a-port")

    assert_raise ArgumentError, ~r/PORT must be an integer/, fn ->
      Config.load!()
    end
  end

  test "rejects invalid FORGE_LOG_LEVEL" do
    System.put_env("PORT", "8080")
    System.put_env("FORGE_LOG_LEVEL", "verbose")

    assert_raise ArgumentError, ~r/FORGE_LOG_LEVEL must be/, fn ->
      Config.load!()
    end
  end

  test "defaults" do
    System.put_env("PORT", "8080")
    System.delete_env("FORGE_SERVICE_NAME")
    System.delete_env("FORGE_SERVICE_VERSION")
    System.delete_env("FORGE_LOG_LEVEL")
    System.delete_env("FORGE_ENV")
    System.delete_env("FORGE_SHUTDOWN_GRACE_SECONDS")

    cfg = Config.load!()
    assert cfg.port == 8080
    assert cfg.service_name == "forge-workflows"
    assert cfg.service_version == "0.1.0"
    assert cfg.log_level == "info"
    assert cfg.env == "development"
    assert cfg.shutdown_grace_ms == 10_000
    assert String.contains?(cfg.database_url, "forge_workflows")
    assert File.dir?(cfg.defs_dir)
    assert cfg.max_parallelism == 8
    assert cfg.default_step_timeout_ms == 300_000
    assert cfg.scheduler_tick_ms == 1_000
  end
end

