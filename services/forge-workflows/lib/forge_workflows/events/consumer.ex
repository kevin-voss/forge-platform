defmodule ForgeWorkflows.Events.Consumer do
  @moduledoc false

  use GenServer

  alias ForgeWorkflows.Clients.EventsClient
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Triggers
  alias ForgeWorkflows.Triggers.Registry

  @default_poll_ms 2_000
  @default_batch 10
  @backoff_ms 5_000

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(opts) do
    enabled = Keyword.get(opts, :enabled, events_enabled?())
    poll_ms = Keyword.get(opts, :poll_ms, @default_poll_ms)
    batch = Keyword.get(opts, :batch, @default_batch)

    state = %{
      enabled: enabled,
      poll_ms: poll_ms,
      batch: batch,
      consumers: %{}
    }

    if enabled do
      {:ok, state, {:continue, :ensure}}
    else
      log("event consumer disabled", %{})
      {:ok, state}
    end
  end

  @impl true
  def handle_continue(:ensure, state) do
    consumers = ensure_consumers(Registry.event_types())
    schedule_poll(state.poll_ms)
    {:noreply, %{state | consumers: consumers}}
  end

  @impl true
  def handle_info(:poll, %{enabled: false} = state), do: {:noreply, state}

  def handle_info(:poll, state) do
    consumers =
      if map_size(state.consumers) == 0 do
        ensure_consumers(Registry.event_types())
      else
        state.consumers
      end

    Enum.each(consumers, fn {event_type, consumer_name} ->
      consume_batch(event_type, consumer_name, state.batch)
    end)

    schedule_poll(state.poll_ms)
    {:noreply, %{state | consumers: consumers}}
  end

  def handle_info(_msg, state), do: {:noreply, state}

  defp ensure_consumers(event_types) do
    Enum.reduce(event_types, %{}, fn event_type, acc ->
      name = consumer_name(event_type)

      case EventsClient.ensure_consumer(name, event_type) do
        :ok ->
          log("event consumer ready", %{consumer: name, event: event_type})
          Map.put(acc, event_type, name)

        {:error, reason} ->
          log("event consumer ensure failed", %{
            consumer: name,
            event: event_type,
            error: inspect(reason)
          })

          acc
      end
    end)
  end

  defp consume_batch(event_type, consumer_name, batch) do
    case EventsClient.consume(consumer_name, batch) do
      {:ok, messages} ->
        Enum.each(messages, &handle_message(event_type, consumer_name, &1))

      {:error, reason} ->
        log("event consume failed", %{
          consumer: consumer_name,
          event: event_type,
          error: inspect(reason)
        })

        Process.sleep(@backoff_ms)
    end
  end

  defp handle_message(event_type, consumer_name, message) when is_map(message) do
    event_id = message["event_id"] || message[:event_id]
    ack_token = message["ack_token"] || message[:ack_token]
    data = message["data"] || message[:data] || %{}
    subject = message["subject"] || message[:subject] || event_type

    project_id = project_id_from(message, data)

    cond do
      not is_binary(event_id) or event_id == "" ->
        _ = EventsClient.ack(ack_token || "")
        log("event missing event_id", %{event: subject})

      not is_binary(ack_token) or ack_token == "" ->
        log("event missing ack_token", %{event_id: event_id, event: subject})

      not is_binary(project_id) or project_id == "" ->
        _ = EventsClient.nak(ack_token, 5)
        log("event missing project_id", %{event_id: event_id, event: subject})

      true ->
        case Triggers.handle_event(subject, event_id, normalize_data(data), project_id) do
          {:ok, :unmatched} ->
            _ = EventsClient.mark_processed(consumer_name, event_id)
            _ = EventsClient.ack(ack_token)

          {:ok, :duplicate, _} ->
            _ = EventsClient.mark_processed(consumer_name, event_id)
            _ = EventsClient.ack(ack_token)

          {:ok, :started, _} ->
            _ = EventsClient.mark_processed(consumer_name, event_id)
            _ = EventsClient.ack(ack_token)

          {:error, reason} ->
            log("event trigger failed", %{
              event_id: event_id,
              event: subject,
              error: inspect(reason)
            })

            _ = EventsClient.nak(ack_token, 5)
        end
    end
  end

  defp handle_message(_, _, _), do: :ok

  defp project_id_from(message, data) do
    Map.get(data, "project_id") ||
      Map.get(message, "project_id") ||
      Map.get(message, "source") ||
      default_project()
  end

  defp normalize_data(data) when is_map(data), do: data
  defp normalize_data(_), do: %{}

  defp consumer_name(event_type) do
    "wf-" <> String.replace(event_type, ".", "-")
  end

  defp schedule_poll(ms), do: Process.send_after(self(), :poll, ms)

  defp events_enabled? do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{events_enabled: false} -> false
      %{events_url: "disabled"} -> false
      %{events_url: ""} -> false
      _ -> true
    end
  end

  defp default_project do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{default_project_id: id} when is_binary(id) and id != "" -> id
      _ -> "default"
    end
  end

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
