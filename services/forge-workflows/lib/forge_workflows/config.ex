defmodule ForgeWorkflows.Config do
  @moduledoc false

  @enforce_keys [:port, :service_name, :service_version, :log_level, :env, :shutdown_grace_ms]
  defstruct [:port, :service_name, :service_version, :log_level, :env, :shutdown_grace_ms]

  @type t :: %__MODULE__{
          port: pos_integer(),
          service_name: String.t(),
          service_version: String.t(),
          log_level: String.t(),
          env: String.t(),
          shutdown_grace_ms: pos_integer()
        }

  @allowed_levels ~w(debug info warn error)

  @spec load!() :: t()
  def load! do
    port = parse_port!(System.get_env("PORT"))
    level = normalize_level!(System.get_env("FORGE_LOG_LEVEL"))
    grace = parse_grace!(System.get_env("FORGE_SHUTDOWN_GRACE_SECONDS"))

    %__MODULE__{
      port: port,
      service_name: blank_default(System.get_env("FORGE_SERVICE_NAME"), "forge-workflows"),
      service_version: blank_default(System.get_env("FORGE_SERVICE_VERSION"), "0.1.0"),
      log_level: level,
      env: blank_default(System.get_env("FORGE_ENV"), "development"),
      shutdown_grace_ms: grace * 1_000
    }
  end

  defp parse_port!(nil), do: raise(ArgumentError, "PORT is required")
  defp parse_port!(""), do: raise(ArgumentError, "PORT is required")

  defp parse_port!(raw) do
    case Integer.parse(String.trim(raw)) do
      {port, ""} when port >= 1 and port <= 65_535 ->
        port

      _ ->
        raise ArgumentError, "PORT must be an integer 1–65535, got #{inspect(raw)}"
    end
  end

  defp parse_grace!(nil), do: 10
  defp parse_grace!(""), do: 10

  defp parse_grace!(raw) do
    case Integer.parse(String.trim(raw)) do
      {seconds, ""} when seconds >= 1 and seconds <= 300 ->
        seconds

      _ ->
        raise ArgumentError,
              "FORGE_SHUTDOWN_GRACE_SECONDS must be an integer 1–300, got #{inspect(raw)}"
    end
  end

  defp normalize_level!(nil), do: "info"
  defp normalize_level!(""), do: "info"

  defp normalize_level!(raw) do
    level = String.downcase(String.trim(raw))

    if level in @allowed_levels do
      level
    else
      raise ArgumentError, "FORGE_LOG_LEVEL must be debug|info|warn|error, got #{inspect(raw)}"
    end
  end

  defp blank_default(nil, default), do: default
  defp blank_default("", default), do: default
  defp blank_default(value, _default), do: String.trim(value)
end
