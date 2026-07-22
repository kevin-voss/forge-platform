defmodule DemoElixirApi.ConfigTest do
  use ExUnit.Case, async: false

  alias DemoElixirApi.Config

  setup do
    original = %{
      "PORT" => System.get_env("PORT"),
      "FORGE_SERVICE_NAME" => System.get_env("FORGE_SERVICE_NAME"),
      "FORGE_SERVICE_VERSION" => System.get_env("FORGE_SERVICE_VERSION"),
      "FORGE_LOG_LEVEL" => System.get_env("FORGE_LOG_LEVEL"),
      "FORGE_ENV" => System.get_env("FORGE_ENV")
    }

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

    cfg = Config.load!()
    assert cfg.port == 8080
    assert cfg.service_name == "demo-elixir-api"
    assert cfg.service_version == "0.1.0"
    assert cfg.log_level == "info"
    assert cfg.env == "development"
  end
end
