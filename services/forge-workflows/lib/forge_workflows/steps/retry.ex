defmodule ForgeWorkflows.Steps.Retry do
  @moduledoc false

  @type policy :: %{
          max_attempts: pos_integer(),
          backoff: String.t(),
          base_ms: non_neg_integer()
        }

  @spec parse_policy(term()) :: {:ok, policy() | nil} | {:error, String.t()}
  def parse_policy(nil), do: {:ok, nil}

  def parse_policy(raw) when is_map(raw) do
    with {:ok, max} <- require_pos_int(raw, "max_attempts"),
         {:ok, backoff} <- require_backoff(raw),
         {:ok, base} <- require_non_neg_int(raw, "base_ms") do
      {:ok, %{max_attempts: max, backoff: backoff, base_ms: base}}
    end
  end

  def parse_policy(_), do: {:error, "retry must be a map"}

  @spec should_retry?(policy(), pos_integer()) :: boolean()
  def should_retry?(%{max_attempts: max}, attempt) when is_integer(attempt) do
    attempt < max
  end

  @spec backoff_ms(policy(), pos_integer()) :: non_neg_integer()
  def backoff_ms(%{backoff: "fixed", base_ms: base}, _attempt), do: base

  def backoff_ms(%{backoff: "exponential", base_ms: base}, attempt)
      when is_integer(attempt) and attempt >= 1 do
    trunc(base * :math.pow(2, attempt - 1))
  end

  @spec schedule(policy(), pos_integer()) ::
          {:retry, non_neg_integer()} | :exhausted
  def schedule(policy, attempt) do
    if should_retry?(policy, attempt) do
      {:retry, backoff_ms(policy, attempt)}
    else
      :exhausted
    end
  end

  defp require_pos_int(map, key) do
    case get(map, key) do
      n when is_integer(n) and n >= 1 -> {:ok, n}
      _ -> {:error, "retry.#{key} must be a positive integer"}
    end
  end

  defp require_non_neg_int(map, key) do
    case get(map, key) do
      n when is_integer(n) and n >= 0 -> {:ok, n}
      _ -> {:error, "retry.#{key} must be a non-negative integer"}
    end
  end

  defp require_backoff(map) do
    case get(map, "backoff") do
      value when value in ["fixed", "exponential"] -> {:ok, value}
      _ -> {:error, "retry.backoff must be fixed|exponential"}
    end
  end

  defp get(map, key) when is_binary(key) do
    Map.get(map, key) || Map.get(map, String.to_atom(key))
  end
end
