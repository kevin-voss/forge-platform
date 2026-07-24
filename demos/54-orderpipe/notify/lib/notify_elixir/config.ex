defmodule NotifyElixir.Config do
  @moduledoc false

  @enforce_keys [
    :port,
    :service_name,
    :service_version,
    :log_level,
    :env,
    :events_url,
    :consumer_name,
    :identity,
    :consume_subject,
    :publish_subject,
    :ack_wait_s,
    :max_deliveries,
    :poll_ms
  ]
  defstruct @enforce_keys

  @type t :: %__MODULE__{
          port: pos_integer(),
          service_name: String.t(),
          service_version: String.t(),
          log_level: String.t(),
          env: String.t(),
          events_url: String.t(),
          consumer_name: String.t(),
          identity: String.t(),
          consume_subject: String.t(),
          publish_subject: String.t(),
          ack_wait_s: pos_integer(),
          max_deliveries: pos_integer(),
          poll_ms: pos_integer()
        }

  @allowed_levels ~w(debug info warn error)

  @spec load!() :: t()
  def load! do
    port = parse_port!(System.get_env("PORT"))
    level = normalize_level!(System.get_env("FORGE_LOG_LEVEL"))
    consumer = blank_default(System.get_env("FORGE_EVENTS_CONSUMER"), "orderpipe-notify")

    %__MODULE__{
      port: port,
      service_name: blank_default(System.get_env("FORGE_SERVICE_NAME"), "orderpipe-notify"),
      service_version: blank_default(System.get_env("FORGE_SERVICE_VERSION"), "0.1.0"),
      log_level: level,
      env: blank_default(System.get_env("FORGE_ENV"), "development"),
      events_url:
        String.trim_trailing(
          blank_default(System.get_env("FORGE_EVENTS_URL"), "http://host.docker.internal:4105"),
          "/"
        ),
      consumer_name: consumer,
      identity: blank_default(System.get_env("FORGE_EVENTS_CONSUMER_IDENTITY"), consumer),
      consume_subject: blank_default(System.get_env("FORGE_EVENTS_SUBJECT"), "order.fulfilled"),
      publish_subject:
        blank_default(System.get_env("FORGE_EVENTS_PUBLISH_SUBJECT"), "order.notified"),
      ack_wait_s: parse_positive!(System.get_env("FORGE_DEFAULT_ACK_WAIT_S"), 30),
      max_deliveries: parse_positive!(System.get_env("FORGE_DEFAULT_MAX_DELIVERIES"), 5),
      poll_ms: parse_positive!(System.get_env("FORGE_EVENTS_POLL_MS"), 400)
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

  defp parse_positive!(nil, default), do: default
  defp parse_positive!("", default), do: default

  defp parse_positive!(raw, default) do
    case Integer.parse(String.trim(raw)) do
      {n, ""} when n > 0 -> n
      _ -> default
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
