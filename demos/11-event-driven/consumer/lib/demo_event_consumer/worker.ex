defmodule DemoEventConsumer.Worker do
  @moduledoc false

  use GenServer

  alias DemoEventConsumer.{EventsClient, Handler, JsonLog}

  def start_link(cfg) do
    GenServer.start_link(__MODULE__, cfg, name: __MODULE__)
  end

  @spec status() :: map()
  def status do
    GenServer.call(__MODULE__, :status)
  end

  @impl true
  def init(cfg) do
    state = %{
      cfg: cfg,
      ready: false,
      processed_ids: MapSet.new(),
      processed_count: 0,
      poison_naks: 0,
      last_error: nil
    }

    send(self(), :ensure_consumer)
    {:ok, state}
  end

  @impl true
  def handle_call(:status, _from, state) do
    reply = %{
      ready: state.ready,
      processed_count: state.processed_count,
      processed_ids: Enum.sort(MapSet.to_list(state.processed_ids)),
      poison_naks: state.poison_naks,
      consumer: state.cfg.consumer_name,
      subject: state.cfg.subject,
      last_error: state.last_error
    }

    {:reply, reply, state}
  end

  @impl true
  def handle_info(:ensure_consumer, state) do
    cfg = state.cfg

    body = %{
      name: cfg.consumer_name,
      subject: cfg.subject,
      ack_wait_s: cfg.ack_wait_s,
      max_deliveries: cfg.max_deliveries,
      identity: cfg.identity
    }

    case EventsClient.create_consumer(cfg.events_url, body) do
      {:ok, status, _} when status in [200, 201] ->
        JsonLog.info(cfg.service_name, "durable consumer ready", %{
          consumer: cfg.consumer_name,
          subject: cfg.subject,
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

    action = fn
      {:nak, msg} ->
        token = Map.fetch!(msg, "ack_token")

        case EventsClient.nak(cfg.events_url, token, 0) do
          {:ok, status, _} when status in [200, 204] ->
            :ok

          {:ok, status, body} ->
            {:error, {:nak_failed, status, body}}

          {:error, reason} ->
            {:error, reason}
        end

      {:ack, msg} ->
        event_id = Map.fetch!(msg, "event_id")
        token = Map.fetch!(msg, "ack_token")

        case EventsClient.mark_processed(cfg.events_url, cfg.consumer_name, event_id) do
          {:ok, status, _} when status in [200, 204] ->
            case EventsClient.ack(cfg.events_url, token) do
              {:ok, ack_status, _} when ack_status in [200, 204] -> :ok
              {:ok, ack_status, body} -> {:error, {:ack_failed, ack_status, body}}
              {:error, reason} -> {:error, reason}
            end

          {:ok, status, body} ->
            {:error, {:mark_processed_failed, status, body}}

          {:error, reason} ->
            {:error, reason}
        end
    end

    case Handler.process_message(message, action) do
      {:ok, :nacked} ->
        JsonLog.info(cfg.service_name, "nacked poison event", %{
          event_id: Map.get(message, "event_id"),
          delivery_count: Map.get(message, "delivery_count")
        })

        %{state | poison_naks: state.poison_naks + 1, last_error: nil}

      {:ok, :acked} ->
        event_id = Map.fetch!(message, "event_id")

        JsonLog.info(cfg.service_name, "acked event", %{event_id: event_id})

        %{
          state
          | processed_ids: MapSet.put(state.processed_ids, event_id),
            processed_count: state.processed_count + 1,
            last_error: nil
        }

      {:error, reason} ->
        JsonLog.error(cfg.service_name, "message handling failed", %{error: inspect(reason)})
        %{state | last_error: inspect(reason)}
    end
  end

  defp schedule_poll(ms) do
    Process.send_after(self(), :poll, ms)
  end
end
