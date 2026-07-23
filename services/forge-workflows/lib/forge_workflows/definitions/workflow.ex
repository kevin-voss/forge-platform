defmodule ForgeWorkflows.Definitions.Workflow do
  @moduledoc false

  @enforce_keys [:name, :steps]
  defstruct [:name, :steps]

  @type step :: %{
          required(:id) => String.t(),
          required(:type) => String.t(),
          optional(:message) => String.t(),
          optional(:delay_ms) => non_neg_integer(),
          optional(:action) => String.t()
        }

  @type t :: %__MODULE__{
          name: String.t(),
          steps: [step()]
        }

  @allowed_types ~w(log noop)

  @spec from_map(map()) :: {:ok, t()} | {:error, String.t()}
  def from_map(raw) when is_map(raw) do
    with {:ok, name} <- require_string(raw, "name"),
         {:ok, steps_raw} <- require_list(raw, "steps"),
         {:ok, steps} <- parse_steps(steps_raw) do
      {:ok, %__MODULE__{name: name, steps: steps}}
    end
  end

  def from_map(_), do: {:error, "workflow definition must be a map"}

  defp parse_steps([]), do: {:error, "steps must be a non-empty list"}

  defp parse_steps(steps) when is_list(steps) do
    steps
    |> Enum.with_index()
    |> Enum.reduce_while({:ok, []}, fn {step, idx}, {:ok, acc} ->
      case parse_step(step, idx) do
        {:ok, parsed} -> {:cont, {:ok, [parsed | acc]}}
        {:error, reason} -> {:halt, {:error, reason}}
      end
    end)
    |> case do
      {:ok, acc} ->
        parsed = Enum.reverse(acc)
        ids = Enum.map(parsed, & &1.id)

        if length(ids) != length(Enum.uniq(ids)) do
          {:error, "duplicate step id"}
        else
          {:ok, parsed}
        end

      other ->
        other
    end
  end

  defp parse_steps(_), do: {:error, "steps must be a list"}

  defp parse_step(step, idx) when is_map(step) do
    with {:ok, id} <- require_string(step, "id"),
         {:ok, type} <- require_string(step, "type"),
         :ok <- validate_type(type, idx),
         {:ok, message} <- optional_string(step, "message"),
         {:ok, delay_ms} <- optional_non_neg_int(step, "delay_ms"),
         {:ok, action} <- optional_string(step, "action") do
      base = %{id: id, type: type}

      base =
        base
        |> maybe_put(:message, message)
        |> maybe_put(:delay_ms, delay_ms)
        |> maybe_put(:action, action)

      {:ok, base}
    end
  end

  defp parse_step(_, idx), do: {:error, "step #{idx} must be a map"}

  defp validate_type(type, _idx) when type in @allowed_types, do: :ok

  defp validate_type(type, idx),
    do: {:error, "step #{idx} type #{inspect(type)} not allowed (log|noop)"}

  defp require_string(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      value when is_binary(value) ->
        trimmed = String.trim(value)

        if trimmed == "" do
          {:error, "#{key} must be a non-empty string"}
        else
          {:ok, trimmed}
        end

      _ ->
        {:error, "#{key} is required"}
    end
  end

  defp require_list(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      value when is_list(value) -> {:ok, value}
      _ -> {:error, "#{key} must be a list"}
    end
  end

  defp optional_string(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      nil -> {:ok, nil}
      value when is_binary(value) -> {:ok, String.trim(value)}
      _ -> {:error, "#{key} must be a string"}
    end
  end

  defp optional_non_neg_int(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      nil ->
        {:ok, nil}

      value when is_integer(value) and value >= 0 ->
        {:ok, value}

      value when is_binary(value) ->
        case Integer.parse(String.trim(value)) do
          {n, ""} when n >= 0 -> {:ok, n}
          _ -> {:error, "#{key} must be a non-negative integer"}
        end

      _ ->
        {:error, "#{key} must be a non-negative integer"}
    end
  end

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)
end
