defmodule DemoElixirApi.JsonLog do
  @moduledoc false

  @level_rank %{"debug" => 10, "info" => 20, "warn" => 30, "error" => 40}

  @spec info(String.t(), String.t(), map()) :: :ok
  def info(service, message, fields \\ %{}) do
    emit(service, "info", message, fields)
  end

  @spec warn(String.t(), String.t(), map()) :: :ok
  def warn(service, message, fields \\ %{}) do
    emit(service, "warn", message, fields)
  end

  @spec error(String.t(), String.t(), map()) :: :ok
  def error(service, message, fields \\ %{}) do
    emit(service, "error", message, fields)
  end

  @spec emit(String.t(), String.t(), String.t(), map()) :: :ok
  def emit(service, level, message, fields \\ %{}) do
    min =
      case Application.get_env(:demo_elixir_api, :runtime_config) do
        %{log_level: configured} -> Map.get(@level_rank, configured, 20)
        _ -> 20
      end

    if Map.get(@level_rank, level, 20) < min do
      :ok
    else
      payload =
        Map.merge(
          %{
            "timestamp" => timestamp(),
            "level" => level,
            "service" => service,
            "message" => message
          },
          stringify_keys(fields)
        )

      IO.puts(Jason.encode!(payload))
    end
  end

  defp timestamp do
    DateTime.utc_now()
    |> DateTime.truncate(:second)
    |> DateTime.to_iso8601()
  end

  defp stringify_keys(map) do
    Map.new(map, fn
      {k, v} when is_atom(k) -> {Atom.to_string(k), v}
      {k, v} -> {k, v}
    end)
  end
end
