defmodule ForgeWorkflows.Config do
  @moduledoc false

  @enforce_keys [
    :port,
    :service_name,
    :service_version,
    :log_level,
    :env,
    :shutdown_grace_ms,
    :database_url,
    :defs_dir,
    :max_parallelism,
    :default_step_timeout_ms,
    :scheduler_tick_ms,
    :events_url,
    :events_enabled,
    :agents_url,
    :agents_mode,
    :agent_poll_ms,
    :agent_step_timeout_ms,
    :default_project_id,
    :events_http_timeout_ms,
    :agents_http_timeout_ms,
    :approval_ttl_seconds,
    :control_url,
    :control_mode,
    :control_http_timeout_ms,
    :report_bucket
  ]
  defstruct [
    :port,
    :service_name,
    :service_version,
    :log_level,
    :env,
    :shutdown_grace_ms,
    :database_url,
    :defs_dir,
    :max_parallelism,
    :default_step_timeout_ms,
    :scheduler_tick_ms,
    :events_url,
    :events_enabled,
    :agents_url,
    :agents_mode,
    :agent_poll_ms,
    :agent_step_timeout_ms,
    :default_project_id,
    :events_http_timeout_ms,
    :agents_http_timeout_ms,
    :approval_ttl_seconds,
    :control_url,
    :control_mode,
    :control_http_timeout_ms,
    :report_bucket
  ]

  @type t :: %__MODULE__{
          port: pos_integer(),
          service_name: String.t(),
          service_version: String.t(),
          log_level: String.t(),
          env: String.t(),
          shutdown_grace_ms: pos_integer(),
          database_url: String.t(),
          defs_dir: String.t(),
          max_parallelism: pos_integer(),
          default_step_timeout_ms: pos_integer(),
          scheduler_tick_ms: pos_integer(),
          events_url: String.t(),
          events_enabled: boolean(),
          agents_url: String.t(),
          agents_mode: String.t(),
          agent_poll_ms: pos_integer(),
          agent_step_timeout_ms: pos_integer(),
          default_project_id: String.t(),
          events_http_timeout_ms: pos_integer(),
          agents_http_timeout_ms: pos_integer(),
          approval_ttl_seconds: pos_integer(),
          control_url: String.t(),
          control_mode: String.t(),
          control_http_timeout_ms: pos_integer(),
          report_bucket: String.t() | nil
        }

  @allowed_levels ~w(debug info warn error)
  @allowed_agents_modes ~w(fake live fail awaiting)
  @allowed_control_modes ~w(fake live fail)

  @spec load!() :: t()
  def load! do
    port = parse_port!(System.get_env("PORT"))
    level = normalize_level!(System.get_env("FORGE_LOG_LEVEL"))
    grace = parse_grace!(System.get_env("FORGE_SHUTDOWN_GRACE_SECONDS"))
    database_url = require_database_url!(System.get_env("FORGE_WORKFLOWS_DATABASE_URL"))
    defs_dir = resolve_defs_dir!(System.get_env("FORGE_WORKFLOWS_DEFS_DIR"))
    events_url = blank_default(System.get_env("FORGE_EVENTS_URL"), "http://forge-events:4105")
    agents_url = blank_default(System.get_env("FORGE_AGENTS_URL"), "http://forge-agents:4301")
    agents_mode = normalize_agents_mode!(System.get_env("FORGE_WORKFLOWS_AGENTS_MODE"))
    control_url = blank_default(System.get_env("FORGE_CONTROL_URL"), "http://forge-control:4001")
    control_mode = normalize_control_mode!(System.get_env("FORGE_WORKFLOWS_CONTROL_MODE"))

    %__MODULE__{
      port: port,
      service_name: blank_default(System.get_env("FORGE_SERVICE_NAME"), "forge-workflows"),
      service_version: blank_default(System.get_env("FORGE_SERVICE_VERSION"), "0.1.0"),
      log_level: level,
      env: blank_default(System.get_env("FORGE_ENV"), "development"),
      shutdown_grace_ms: grace * 1_000,
      database_url: database_url,
      defs_dir: defs_dir,
      max_parallelism: parse_pos_int!(System.get_env("FORGE_WORKFLOWS_MAX_PARALLELISM"), 8, 1, 64),
      default_step_timeout_ms:
        parse_pos_int!(
          System.get_env("FORGE_WORKFLOWS_DEFAULT_STEP_TIMEOUT"),
          300_000,
          1,
          3_600_000
        ),
      scheduler_tick_ms:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_SCHEDULER_TICK_MS"), 1_000, 50, 60_000),
      events_url: events_url,
      events_enabled: events_enabled?(events_url, System.get_env("FORGE_WORKFLOWS_EVENTS_ENABLED")),
      agents_url: agents_url,
      agents_mode: agents_mode,
      agent_poll_ms:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_AGENT_POLL_MS"), 1_000, 50, 60_000),
      agent_step_timeout_ms:
        parse_pos_int!(
          System.get_env("FORGE_WORKFLOWS_AGENT_STEP_TIMEOUT"),
          300_000,
          1,
          3_600_000
        ),
      default_project_id:
        blank_default(System.get_env("FORGE_WORKFLOWS_DEFAULT_PROJECT"), "default"),
      events_http_timeout_ms:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_EVENTS_HTTP_TIMEOUT_MS"), 10_000, 100, 120_000),
      agents_http_timeout_ms:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_AGENTS_HTTP_TIMEOUT_MS"), 10_000, 100, 120_000),
      approval_ttl_seconds:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_APPROVAL_TTL_SECONDS"), 86_400, 1, 2_592_000),
      control_url: control_url,
      control_mode: control_mode,
      control_http_timeout_ms:
        parse_pos_int!(System.get_env("FORGE_WORKFLOWS_CONTROL_HTTP_TIMEOUT_MS"), 10_000, 100, 120_000),
      report_bucket: blank_or_nil(System.get_env("FORGE_WORKFLOWS_REPORT_BUCKET"))
    }
  end

  defp events_enabled?(_url, "0"), do: false
  defp events_enabled?(_url, "false"), do: false
  defp events_enabled?(_url, "FALSE"), do: false
  defp events_enabled?("disabled", _), do: false
  defp events_enabled?("", _), do: false
  defp events_enabled?(_, _), do: true

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

  defp parse_pos_int!(nil, default, _min, _max), do: default
  defp parse_pos_int!("", default, _min, _max), do: default

  defp parse_pos_int!(raw, _default, min, max) do
    case Integer.parse(String.trim(raw)) do
      {n, ""} when n >= min and n <= max ->
        n

      _ ->
        raise ArgumentError,
              "expected integer #{min}–#{max}, got #{inspect(raw)}"
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

  defp normalize_agents_mode!(nil), do: "fake"
  defp normalize_agents_mode!(""), do: "fake"

  defp normalize_agents_mode!(raw) do
    mode = String.downcase(String.trim(raw))

    if mode in @allowed_agents_modes do
      mode
    else
      raise ArgumentError,
            "FORGE_WORKFLOWS_AGENTS_MODE must be fake|live|fail|awaiting, got #{inspect(raw)}"
    end
  end

  defp normalize_control_mode!(nil), do: "fake"
  defp normalize_control_mode!(""), do: "fake"

  defp normalize_control_mode!(raw) do
    mode = String.downcase(String.trim(raw))

    if mode in @allowed_control_modes do
      mode
    else
      raise ArgumentError,
            "FORGE_WORKFLOWS_CONTROL_MODE must be fake|live|fail, got #{inspect(raw)}"
    end
  end

  defp blank_or_nil(nil), do: nil
  defp blank_or_nil(""), do: nil
  defp blank_or_nil(value), do: String.trim(value)

  defp require_database_url!(nil),
    do: raise(ArgumentError, "FORGE_WORKFLOWS_DATABASE_URL is required")

  defp require_database_url!(""),
    do: raise(ArgumentError, "FORGE_WORKFLOWS_DATABASE_URL is required")

  defp require_database_url!(raw) do
    url = String.trim(raw)

    if String.starts_with?(url, "postgres://") or String.starts_with?(url, "postgresql://") do
      url
    else
      raise ArgumentError,
            "FORGE_WORKFLOWS_DATABASE_URL must be a postgres URL, got #{inspect(raw)}"
    end
  end

  defp resolve_defs_dir!(nil), do: find_defs_dir!()
  defp resolve_defs_dir!(""), do: find_defs_dir!()

  defp resolve_defs_dir!(raw) do
    path = Path.expand(String.trim(raw))

    if File.dir?(path) do
      path
    else
      raise ArgumentError, "FORGE_WORKFLOWS_DEFS_DIR is not a directory: #{inspect(path)}"
    end
  end

  defp find_defs_dir! do
    candidates = [
      Path.expand("definitions", File.cwd!()),
      Path.expand("../../definitions", __DIR__),
      "/app/definitions"
    ]

    case Enum.find(candidates, &File.dir?/1) do
      nil ->
        raise ArgumentError,
              "FORGE_WORKFLOWS_DEFS_DIR not set and no definitions/ directory found"

      path ->
        path
    end
  end

  defp blank_default(nil, default), do: default
  defp blank_default("", default), do: default
  defp blank_default(value, _default), do: String.trim(value)
end
