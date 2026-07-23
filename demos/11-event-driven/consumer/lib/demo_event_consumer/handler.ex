defmodule DemoEventConsumer.Handler do
  @moduledoc false

  @doc """
  Decide ack vs nak for a delivered message.

  Poison events (`data.reason == "poison"`) are nacked so they retry into the DLQ.
  All other events are marked processed then acked.
  """
  @spec classify(map()) :: :ack | :nak
  def classify(%{"data" => data}) when is_map(data) do
    case Map.get(data, "reason") do
      "poison" -> :nak
      _ -> :ack
    end
  end

  def classify(_), do: :ack

  @spec process_message(map(), (map() -> :ok | {:error, term()})) ::
          {:ok, :acked | :nacked} | {:error, term()}
  def process_message(message, action_fun) when is_map(message) and is_function(action_fun, 1) do
    case classify(message) do
      :nak ->
        case action_fun.({:nak, message}) do
          :ok -> {:ok, :nacked}
          {:error, reason} -> {:error, reason}
        end

      :ack ->
        case action_fun.({:ack, message}) do
          :ok -> {:ok, :acked}
          {:error, reason} -> {:error, reason}
        end
    end
  end
end
