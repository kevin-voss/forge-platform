defmodule ForgeWorkflows.Definitions.Workflow do
  @moduledoc false

  alias ForgeWorkflows.Steps.Retry

  @enforce_keys [:name, :steps]
  defstruct [:name, :steps, :timeout_ms, :trigger]

  @type retry_policy :: %{
          max_attempts: pos_integer(),
          backoff: String.t(),
          base_ms: non_neg_integer()
        }

  @type trigger :: %{event: String.t()}

  @type step :: %{
          required(:id) => String.t(),
          required(:type) => String.t(),
          optional(:message) => String.t(),
          optional(:delay_ms) => non_neg_integer(),
          optional(:action) => String.t(),
          optional(:retry) => retry_policy(),
          optional(:timeout_ms) => pos_integer(),
          optional(:branches) => [step()],
          optional(:when) => String.t(),
          optional(:then) => String.t(),
          optional(:else) => String.t(),
          optional(:succeed_on_attempt) => pos_integer(),
          optional(:agent) => String.t(),
          optional(:input) => map(),
          optional(:prompt) => String.t(),
          optional(:on_deny) => String.t(),
          optional(:compensate) => String.t()
        }

  @type t :: %__MODULE__{
          name: String.t(),
          steps: [step()],
          timeout_ms: pos_integer() | nil,
          trigger: trigger() | nil
        }

  @allowed_types ~w(log noop task delay timeout parallel conditional retry agent approval)

  @spec from_map(map()) :: {:ok, t()} | {:error, String.t()}
  def from_map(raw) when is_map(raw) do
    with {:ok, name} <- require_string(raw, "name"),
         {:ok, steps_raw} <- require_list(raw, "steps"),
         {:ok, timeout_ms} <- optional_pos_int(raw, "timeout_ms"),
         {:ok, trigger} <- parse_trigger(raw),
         {:ok, steps} <- parse_steps(steps_raw),
         :ok <- validate_references(steps) do
      {:ok, %__MODULE__{name: name, steps: steps, timeout_ms: timeout_ms, trigger: trigger}}
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
        ids = collect_ids(parsed)

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
         {:ok, action} <- optional_string(step, "action"),
         {:ok, retry} <- parse_retry(step),
         {:ok, timeout_ms} <- optional_pos_int(step, "timeout_ms"),
         {:ok, when_expr} <- optional_string(step, "when"),
         {:ok, then_id} <- optional_string(step, "then"),
         {:ok, else_id} <- optional_string(step, "else"),
         {:ok, succeed_on} <- optional_pos_int(step, "succeed_on_attempt"),
         {:ok, agent} <- optional_string(step, "agent"),
         {:ok, input} <- optional_map(step, "input"),
         {:ok, prompt} <- optional_string(step, "prompt"),
         {:ok, on_deny} <- optional_string(step, "on_deny"),
         {:ok, compensate} <- optional_string(step, "compensate"),
         {:ok, branches} <- parse_branches(step, idx),
         :ok <-
           validate_typed_fields(
             type,
             delay_ms,
             when_expr,
             then_id,
             else_id,
             branches,
             timeout_ms,
             agent,
             prompt,
             idx
           ) do
      base = %{id: id, type: type}

      base =
        base
        |> maybe_put(:message, message)
        |> maybe_put(:delay_ms, delay_ms)
        |> maybe_put(:action, action)
        |> maybe_put(:retry, retry)
        |> maybe_put(:timeout_ms, timeout_ms)
        |> maybe_put(:when, when_expr)
        |> maybe_put(:then, then_id)
        |> maybe_put(:else, else_id)
        |> maybe_put(:succeed_on_attempt, succeed_on)
        |> maybe_put(:agent, agent)
        |> maybe_put(:input, input)
        |> maybe_put(:prompt, prompt)
        |> maybe_put(:on_deny, on_deny)
        |> maybe_put(:compensate, compensate)
        |> maybe_put(:branches, branches)

      {:ok, base}
    end
  end

  defp parse_step(_, idx), do: {:error, "step #{idx} must be a map"}

  defp parse_trigger(raw) do
    case Map.get(raw, "trigger") || Map.get(raw, :trigger) do
      nil ->
        {:ok, nil}

      trigger when is_map(trigger) ->
        case require_string(trigger, "event") do
          {:ok, event} -> {:ok, %{event: event}}
          {:error, _} -> {:error, "trigger.event is required"}
        end

      _ ->
        {:error, "trigger must be a map"}
    end
  end

  defp parse_retry(step) do
    case Map.get(step, "retry") || Map.get(step, :retry) do
      nil -> {:ok, nil}
      raw -> Retry.parse_policy(raw)
    end
  end

  defp parse_branches(step, idx) do
    case Map.get(step, "branches") || Map.get(step, :branches) do
      nil ->
        {:ok, nil}

      list when is_list(list) ->
        list
        |> Enum.with_index()
        |> Enum.reduce_while({:ok, []}, fn {branch, bidx}, {:ok, acc} ->
          case parse_branch(branch, idx, bidx) do
            {:ok, parsed} -> {:cont, {:ok, [parsed | acc]}}
            {:error, reason} -> {:halt, {:error, reason}}
          end
        end)
        |> case do
          {:ok, acc} -> {:ok, Enum.reverse(acc)}
          other -> other
        end

      _ ->
        {:error, "step #{idx} branches must be a list"}
    end
  end

  defp parse_branch(branch, idx, _bidx) when is_binary(branch) do
    trimmed = String.trim(branch)

    if trimmed == "" do
      {:error, "step #{idx} branch id must be non-empty"}
    else
      # Resolved later against sibling step ids — store as reference stub.
      {:ok, %{id: trimmed, type: "ref"}}
    end
  end

  defp parse_branch(branch, idx, bidx) when is_map(branch) do
    parse_step(branch, :"#{idx}.#{bidx}")
  end

  defp parse_branch(_, idx, _), do: {:error, "step #{idx} branch must be a map or string id"}

  defp validate_typed_fields("delay", delay_ms, _, _, _, _, _, _, _, idx) do
    if is_integer(delay_ms) and delay_ms >= 0 do
      :ok
    else
      {:error, "step #{idx} type delay requires delay_ms"}
    end
  end

  defp validate_typed_fields("conditional", _, when_expr, then_id, else_id, _, _, _, _, idx) do
    cond do
      not is_binary(when_expr) or when_expr == "" ->
        {:error, "step #{idx} type conditional requires when"}

      not is_binary(then_id) or then_id == "" ->
        {:error, "step #{idx} type conditional requires then"}

      not is_binary(else_id) or else_id == "" ->
        {:error, "step #{idx} type conditional requires else"}

      true ->
        :ok
    end
  end

  defp validate_typed_fields("parallel", _, _, _, _, branches, _, _, _, idx) do
    if is_list(branches) and branches != [] do
      :ok
    else
      {:error, "step #{idx} type parallel requires non-empty branches"}
    end
  end

  defp validate_typed_fields("timeout", _, _, _, _, _, timeout_ms, _, _, idx) do
    if is_integer(timeout_ms) and timeout_ms >= 1 do
      :ok
    else
      {:error, "step #{idx} type timeout requires timeout_ms"}
    end
  end

  defp validate_typed_fields("agent", _, _, _, _, _, _, agent, _, idx) do
    if is_binary(agent) and agent != "" do
      :ok
    else
      {:error, "step #{idx} type agent requires agent"}
    end
  end

  defp validate_typed_fields("approval", _, _, _, _, _, _, _, prompt, idx) do
    if is_binary(prompt) and prompt != "" do
      :ok
    else
      {:error, "step #{idx} type approval requires prompt"}
    end
  end

  defp validate_typed_fields(_, _, _, _, _, _, _, _, _, _), do: :ok

  defp validate_references(steps) do
    ids = MapSet.new(Enum.map(steps, & &1.id))

    Enum.reduce_while(steps, :ok, fn step, :ok ->
      case step.type do
        "conditional" ->
          cond do
            not MapSet.member?(ids, step.then) ->
              {:halt, {:error, "conditional #{step.id} then #{inspect(step.then)} not found"}}

            not MapSet.member?(ids, step.else) ->
              {:halt, {:error, "conditional #{step.id} else #{inspect(step.else)} not found"}}

            true ->
              {:cont, :ok}
          end

        "approval" ->
          case Map.get(step, :on_deny) do
            nil ->
              {:cont, :ok}

            target ->
              if MapSet.member?(ids, target) do
                {:cont, :ok}
              else
                {:halt, {:error, "approval #{step.id} on_deny #{inspect(target)} not found"}}
              end
          end

        "parallel" ->
          case resolve_parallel_refs(step, steps) do
            :ok -> {:cont, :ok}
            {:error, reason} -> {:halt, {:error, reason}}
          end

        _ ->
          {:cont, :ok}
      end
    end)
  end

  defp resolve_parallel_refs(%{branches: branches} = step, all_steps) do
    by_id = Map.new(all_steps, &{&1.id, &1})

    Enum.reduce_while(branches, :ok, fn branch, :ok ->
      if branch.type == "ref" do
        case Map.get(by_id, branch.id) do
          nil -> {:halt, {:error, "parallel #{step.id} branch #{inspect(branch.id)} not found"}}
          _ -> {:cont, :ok}
        end
      else
        {:cont, :ok}
      end
    end)
  end

  defp collect_ids(steps) do
    Enum.flat_map(steps, fn step ->
      branch_ids =
        case Map.get(step, :branches) do
          list when is_list(list) ->
            Enum.flat_map(list, fn
              %{type: "ref"} -> []
              %{id: id} -> [id]
            end)

          _ ->
            []
        end

      [step.id | branch_ids]
    end)
  end

  defp validate_type(type, _idx) when type in @allowed_types, do: :ok

  defp validate_type(type, idx),
    do:
      {:error,
       "step #{idx} type #{inspect(type)} not allowed (log|noop|task|delay|timeout|parallel|conditional|retry|agent|approval)"}

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

  defp optional_map(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      nil -> {:ok, nil}
      value when is_map(value) -> {:ok, value}
      _ -> {:error, "#{key} must be a map"}
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

  defp optional_pos_int(map, key) do
    case Map.get(map, key) || Map.get(map, String.to_atom(key)) do
      nil ->
        {:ok, nil}

      value when is_integer(value) and value >= 1 ->
        {:ok, value}

      value when is_binary(value) ->
        case Integer.parse(String.trim(value)) do
          {n, ""} when n >= 1 -> {:ok, n}
          _ -> {:error, "#{key} must be a positive integer"}
        end

      _ ->
        {:error, "#{key} must be a positive integer"}
    end
  end

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)
end
