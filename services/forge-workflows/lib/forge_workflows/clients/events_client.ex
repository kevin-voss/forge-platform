defmodule ForgeWorkflows.Clients.EventsClient do
  @moduledoc false

  alias ForgeWorkflows.Clients.Http

  @callback ensure_consumer(String.t(), String.t(), keyword()) :: :ok | {:error, term()}
  @callback consume(String.t(), pos_integer()) :: {:ok, [map()]} | {:error, term()}
  @callback ack(String.t()) :: :ok | {:error, term()}
  @callback nak(String.t(), non_neg_integer()) :: :ok | {:error, term()}
  @callback mark_processed(String.t(), String.t()) :: :ok | {:error, term()}

  @spec ensure_consumer(String.t(), String.t(), keyword()) :: :ok | {:error, term()}
  def ensure_consumer(name, subject, opts \\ [])
      when is_binary(name) and is_binary(subject) and is_list(opts) do
    impl().ensure_consumer(name, subject, opts)
  end

  @spec consume(String.t(), pos_integer()) :: {:ok, [map()]} | {:error, term()}
  def consume(name, batch \\ 10) when is_binary(name) and is_integer(batch) do
    impl().consume(name, batch)
  end

  @spec ack(String.t()) :: :ok | {:error, term()}
  def ack(ack_token) when is_binary(ack_token), do: impl().ack(ack_token)

  @spec nak(String.t(), non_neg_integer()) :: :ok | {:error, term()}
  def nak(ack_token, delay_s \\ 5)
      when is_binary(ack_token) and is_integer(delay_s),
      do: impl().nak(ack_token, delay_s)

  @spec mark_processed(String.t(), String.t()) :: :ok | {:error, term()}
  def mark_processed(consumer, event_id)
      when is_binary(consumer) and is_binary(event_id),
      do: impl().mark_processed(consumer, event_id)

  defp impl do
    Application.get_env(
      :forge_workflows,
      :events_client,
      ForgeWorkflows.Clients.EventsClient.HTTP
    )
  end
end

defmodule ForgeWorkflows.Clients.EventsClient.HTTP do
  @moduledoc false

  @behaviour ForgeWorkflows.Clients.EventsClient

  alias ForgeWorkflows.Clients.Http

  @impl true
  def ensure_consumer(name, subject, opts) do
    body =
      Jason.encode!(%{
        "name" => name,
        "subject" => subject,
        "ack_wait_s" => Keyword.get(opts, :ack_wait_s, 30),
        "max_deliveries" => Keyword.get(opts, :max_deliveries, 5)
      })

    case Http.request(:post, url("/v1/consumers"), headers(), body, timeout_ms()) do
      {:ok, status, _} when status in [200, 201] -> :ok
      {:ok, 409, _} -> :ok
      {:ok, status, resp} -> {:error, {:http, status, resp}}
      {:error, reason} -> {:error, reason}
    end
  end

  @impl true
  def consume(name, batch) do
    body = Jason.encode!(%{"consumer" => name, "batch" => batch})

    case Http.request(:post, url("/v1/consume"), headers(), body, timeout_ms()) do
      {:ok, 200, resp} ->
        case Jason.decode(resp) do
          {:ok, %{"messages" => messages}} when is_list(messages) -> {:ok, messages}
          {:ok, _} -> {:ok, []}
          {:error, reason} -> {:error, reason}
        end

      {:ok, status, resp} ->
        {:error, {:http, status, resp}}

      {:error, reason} ->
        {:error, reason}
    end
  end

  @impl true
  def ack(ack_token) do
    body = Jason.encode!(%{"ack_token" => ack_token})

    case Http.request(:post, url("/v1/ack"), headers(), body, timeout_ms()) do
      {:ok, status, _} when status in [200, 204] -> :ok
      {:ok, status, resp} -> {:error, {:http, status, resp}}
      {:error, reason} -> {:error, reason}
    end
  end

  @impl true
  def nak(ack_token, delay_s) do
    body = Jason.encode!(%{"ack_token" => ack_token, "delay_s" => delay_s})

    case Http.request(:post, url("/v1/nak"), headers(), body, timeout_ms()) do
      {:ok, status, _} when status in [200, 204] -> :ok
      {:ok, status, resp} -> {:error, {:http, status, resp}}
      {:error, reason} -> {:error, reason}
    end
  end

  @impl true
  def mark_processed(consumer, event_id) do
    body = Jason.encode!(%{"consumer" => consumer, "event_id" => event_id})

    case Http.request(:post, url("/v1/processed"), headers(), body, timeout_ms()) do
      {:ok, status, _} when status in [200, 204] -> :ok
      {:ok, status, resp} -> {:error, {:http, status, resp}}
      {:error, reason} -> {:error, reason}
    end
  end

  defp url(path) do
    base =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{events_url: url} when is_binary(url) -> String.trim_trailing(url, "/")
        _ -> "http://forge-events:4105"
      end

    base <> path
  end

  defp headers do
    [{"content-type", "application/json"}, {"accept", "application/json"}]
  end

  defp timeout_ms do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{events_http_timeout_ms: ms} when is_integer(ms) -> ms
      _ -> 10_000
    end
  end
end

defmodule ForgeWorkflows.Clients.EventsClient.Noop do
  @moduledoc false

  @behaviour ForgeWorkflows.Clients.EventsClient

  @impl true
  def ensure_consumer(_name, _subject, _opts), do: :ok

  @impl true
  def consume(_name, _batch), do: {:ok, []}

  @impl true
  def ack(_ack_token), do: :ok

  @impl true
  def nak(_ack_token, _delay_s), do: :ok

  @impl true
  def mark_processed(_consumer, _event_id), do: :ok
end
