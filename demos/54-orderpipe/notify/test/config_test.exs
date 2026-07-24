defmodule NotifyElixir.ConfigTest do
  use ExUnit.Case, async: false

  alias NotifyElixir.Config

  setup do
    previous = %{
      "PORT" => System.get_env("PORT"),
      "FORGE_SERVICE_NAME" => System.get_env("FORGE_SERVICE_NAME"),
      "FORGE_SERVICE_VERSION" => System.get_env("FORGE_SERVICE_VERSION"),
      "FORGE_LOG_LEVEL" => System.get_env("FORGE_LOG_LEVEL"),
      "FORGE_ENV" => System.get_env("FORGE_ENV")
    }

    on_exit(fn ->
      Enum.each(previous, fn {k, v} ->
        if v, do: System.put_env(k, v), else: System.delete_env(k)
      end)
    end)

    :ok
  end

  test "load defaults service name to orderpipe-notify" do
    System.put_env("PORT", "8080")
    System.delete_env("FORGE_SERVICE_NAME")
    cfg = Config.load!()
    assert cfg.service_name == "orderpipe-notify"
    assert cfg.port == 8080
  end

  test "requires PORT" do
    System.delete_env("PORT")
    assert_raise ArgumentError, ~r/PORT is required/, fn -> Config.load!() end
  end
end
