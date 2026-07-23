defmodule NotifyElixir.Config do
  @moduledoc false

  @enforce_keys [:port, :service_name, :service_version, :log_level, :env]
  defstruct [:port, :service_name, :service_version, :log_level, :env, capstone_break: false]

  @type t :: %__MODULE__{
          port: pos_integer(),
          service_name: String.t(),
          service_version: String.t(),
          log_level: String.t(),
          env: String.t(),
          capstone_break: boolean()
        }

  @allowed_levels ~w(debug info warn error)

  @spec load!() :: t()
  def load! do
    port = parse_port!(System.get_env("PORT"))
    level = normalize_level!(System.get_env("FORGE_LOG_LEVEL"))

    %__MODULE__{
      port: port,
      service_name: blank_default(System.get_env("FORGE_SERVICE_NAME"), "incident-notify"),
      service_version: blank_default(System.get_env("FORGE_SERVICE_VERSION"), "0.1.0"),
      log_level: level,
      env: blank_default(System.get_env("FORGE_ENV"), "development"),
      capstone_break: truthy?(System.get_env("CAPSTONE_BREAK"))
    }
  end

  defp truthy?(nil), do: false
  defp truthy?(""), do: false

  defp truthy?(raw) do
    String.downcase(String.trim(raw)) in ~w(1 true yes on)
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
