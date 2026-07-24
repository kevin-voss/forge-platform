defmodule NotifyElixir.EventsClient do
  @moduledoc false

  @type response :: {:ok, non_neg_integer(), map() | list() | nil} | {:error, term()}

  @spec ping(String.t()) :: response()
  def ping(base_url) when is_binary(base_url) do
    get_json(base_url <> "/health/ready")
  end

  @spec create_consumer(String.t(), map()) :: response()
  def create_consumer(base_url, body) when is_binary(base_url) and is_map(body) do
    post_json(base_url <> "/v1/consumers", body)
  end

  @spec consume(String.t(), String.t(), pos_integer()) :: response()
  def consume(base_url, consumer, batch) do
    post_json(base_url <> "/v1/consume", %{consumer: consumer, batch: batch})
  end

  @spec ack(String.t(), String.t()) :: response()
  def ack(base_url, ack_token) do
    post_json(base_url <> "/v1/ack", %{ack_token: ack_token})
  end

  @spec nak(String.t(), String.t(), non_neg_integer()) :: response()
  def nak(base_url, ack_token, delay_s) do
    post_json(base_url <> "/v1/nak", %{ack_token: ack_token, delay_s: delay_s})
  end

  @spec mark_processed(String.t(), String.t(), String.t()) :: response()
  def mark_processed(base_url, consumer, event_id) do
    post_json(base_url <> "/v1/processed", %{consumer: consumer, event_id: event_id})
  end

  @spec publish(String.t(), map(), String.t()) :: response()
  def publish(base_url, body, idem_key)
      when is_binary(base_url) and is_map(body) and is_binary(idem_key) do
    post_json(base_url <> "/v1/events", body, [{"idempotency-key", idem_key}])
  end

  defp get_json(url) do
    request = {String.to_charlist(url), [{~c"accept", ~c"application/json"}]}

    case :httpc.request(:get, request, [{:timeout, 10_000}], [{:body_format, :binary}]) do
      {:ok, {{_, status, _}, _headers, resp_body}} ->
        {:ok, status, decode_body(resp_body)}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp post_json(url, body, extra_headers \\ []) do
    headers =
      [{~c"content-type", ~c"application/json"}] ++
        Enum.map(extra_headers, fn {k, v} -> {String.to_charlist(k), String.to_charlist(v)} end)

    payload = Jason.encode!(body)

    request = {
      String.to_charlist(url),
      headers,
      ~c"application/json",
      payload
    }

    case :httpc.request(:post, request, [{:timeout, 15_000}], [{:body_format, :binary}]) do
      {:ok, {{_, status, _}, _headers, resp_body}} ->
        {:ok, status, decode_body(resp_body)}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp decode_body(""), do: nil

  defp decode_body(body) when is_binary(body) do
    case Jason.decode(body) do
      {:ok, decoded} -> decoded
      {:error, _} -> %{"raw" => body}
    end
  end
end
