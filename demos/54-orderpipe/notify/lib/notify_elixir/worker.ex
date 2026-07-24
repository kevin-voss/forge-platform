defmodule NotifyElixir.Worker do
  @moduledoc false

  use GenServer

  alias NotifyElixir.{EventsClient, JsonLog, Store}

  def start_link(cfg) do
    GenServer.start_link(__MODULE__, cfg, name: __MODULE__)
  end

  @impl true
  def init(cfg) do
    state = %{
      cfg: cfg,
      ready: false,
      last_error: nil
    }

    send(self(), :ensure_consumer)
    {:ok, state}
  end

  @impl true
  def handle_info(:ensure_consumer, state) do
    cfg = state.cfg

    body = %{
      name: cfg.consumer_name,
      subject: cfg.consume_subject,
      ack_wait_s: cfg.ack_wait_s,
      max_deliveries: cfg.max_deliveries,
      identity: cfg.identity
    }

    case EventsClient.create_consumer(cfg.events_url, body) do
      {:ok, status, _} when status in [200, 201] ->
        JsonLog.info(cfg.service_name, "durable consumer ready", %{
          consumer: cfg.consumer_name,
          subject: cfg.consume_subject,
          status: status
        })

        schedule_poll(cfg.poll_ms)
        {:noreply, %{state | ready: true, last_error: nil}}

      {:ok, status, body} ->
        JsonLog.warn(cfg.service_name, "create consumer retry", %{status: status, body: body})
        Process.send_after(self(), :ensure_consumer, 1000)
        {:noreply, %{state | last_error: "create_consumer_http_#{status}"}}

      {:error, reason} ->
        JsonLog.warn(cfg.service_name, "create consumer error", %{error: inspect(reason)})
        Process.send_after(self(), :ensure_consumer, 1000)
        {:noreply, %{state | last_error: inspect(reason)}}
    end
  end

  def handle_info(:poll, state) do
    state = consume_batch(state)
    schedule_poll(state.cfg.poll_ms)
    {:noreply, state}
  end

  defp consume_batch(%{ready: false} = state), do: state

  defp consume_batch(state) do
    cfg = state.cfg

    case EventsClient.consume(cfg.events_url, cfg.consumer_name, 10) do
      {:ok, 200, %{"messages" => messages}} when is_list(messages) ->
        Enum.reduce(messages, state, &handle_one/2)

      {:ok, 200, _} ->
        state

      {:ok, status, body} ->
        JsonLog.warn(cfg.service_name, "consume failed", %{status: status, body: body})
        %{state | last_error: "consume_http_#{status}"}

      {:error, reason} ->
        JsonLog.warn(cfg.service_name, "consume error", %{error: inspect(reason)})
        %{state | last_error: inspect(reason)}
    end
  end

  defp handle_one(message, state) do
    cfg = state.cfg
    event_id = Map.get(message, "event_id") || ""
    ack_token = Map.get(message, "ack_token") || ""
    data = Map.get(message, "data") || %{}

    data =
      cond do
        is_map(data) -> data
        is_binary(data) ->
          case Jason.decode(data) do
            {:ok, decoded} when is_map(decoded) -> decoded
            _ -> %{}
          end

        true ->
          %{}
      end

    order_id = Map.get(data, "order_id") || ""

    if is_binary(order_id) and order_id != "" do
      note = %{
        "id" => Base.encode16(:crypto.strong_rand_bytes(8), case: :lower),
        "orderId" => order_id,
        "channel" => "email",
        "message" => "Order #{order_id} fulfilled",
        "status" => "queued"
      }

      Store.put(note)

      email = Map.get(data, "customer_email") || "unknown@example.com"
      total = Map.get(data, "total_cents") || 0

      occurred_at =
        DateTime.utc_now() |> DateTime.truncate(:second) |> DateTime.to_iso8601()

      pub = %{
        subject: cfg.publish_subject,
        source: cfg.service_name,
        data: %{
          order_id: order_id,
          customer_email: email,
          status: "notified",
          total_cents: total,
          occurred_at: occurred_at,
          source: cfg.service_name
        }
      }

      case EventsClient.publish(cfg.events_url, pub, "#{order_id}:#{cfg.publish_subject}") do
        {:ok, 202, _} ->
          case EventsClient.mark_processed(cfg.events_url, cfg.consumer_name, event_id) do
            {:ok, status, _} when status in [200, 204] ->
              case EventsClient.ack(cfg.events_url, ack_token) do
                {:ok, ack_status, _} when ack_status in [200, 204] ->
                  JsonLog.info(cfg.service_name, "order notified", %{orderId: order_id})
                  state

                {:ok, ack_status, body} ->
                  JsonLog.warn(cfg.service_name, "ack failed", %{status: ack_status, body: body})
                  %{state | last_error: "ack_http_#{ack_status}"}

                {:error, reason} ->
                  %{state | last_error: inspect(reason)}
              end

            {:ok, status, body} ->
              JsonLog.warn(cfg.service_name, "mark_processed failed", %{status: status, body: body})
              %{state | last_error: "processed_http_#{status}"}

            {:error, reason} ->
              %{state | last_error: inspect(reason)}
          end

        {:ok, status, body} ->
          JsonLog.warn(cfg.service_name, "publish notified failed", %{status: status, body: body})
          _ = EventsClient.nak(cfg.events_url, ack_token, 1)
          %{state | last_error: "publish_http_#{status}"}

        {:error, reason} ->
          _ = EventsClient.nak(cfg.events_url, ack_token, 1)
          %{state | last_error: inspect(reason)}
      end
    else
      _ = EventsClient.mark_processed(cfg.events_url, cfg.consumer_name, event_id)
      _ = EventsClient.ack(cfg.events_url, ack_token)
      state
    end
  end

  defp schedule_poll(ms) when is_integer(ms) and ms > 0 do
    Process.send_after(self(), :poll, ms)
  end

  defp schedule_poll(_), do: Process.send_after(self(), :poll, 400)
end
