defmodule ForgeWorkflows.Steps.Conditional do
  @moduledoc false

  alias ForgeWorkflows.JsonLog

  @type result :: {:ok, String.t(), boolean()} | {:error, String.t()}

  @spec select(map(), map()) :: result()
  def select(step_def, context) when is_map(step_def) and is_map(context) do
    when_expr = Map.get(step_def, :when) || Map.get(step_def, "when")
    then_id = Map.get(step_def, :then) || Map.get(step_def, "then")
    else_id = Map.get(step_def, :else) || Map.get(step_def, "else")

    with {:ok, truthy} <- evaluate(when_expr, context) do
      branch = if truthy, do: then_id, else: else_id

      log("conditional branch", %{
        step_id: Map.get(step_def, :id) || Map.get(step_def, "id"),
        when: when_expr,
        result: truthy,
        branch: branch
      })

      {:ok, branch, truthy}
    end
  end

  @doc """
  Safe predicate evaluator — no arbitrary code.

  Supported forms:
  * `context.<key>` — truthy check
  * `context.<key> == '<value>'` / `== "<value>"`
  * `context.<key> != '<value>'` / `!= "<value>"`
  """
  @spec evaluate(String.t(), map()) :: {:ok, boolean()} | {:error, String.t()}
  def evaluate(expr, context) when is_binary(expr) and is_map(context) do
    trimmed = String.trim(expr)

    cond do
      trimmed == "" ->
        {:error, "conditional when expression is empty"}

      match = Regex.run(~r/^context\.([A-Za-z_][A-Za-z0-9_]*)\s*==\s*'(.*)'$/, trimmed) ->
        [_, key, expected] = match
        {:ok, to_string(context_get(context, key)) == expected}

      match = Regex.run(~r/^context\.([A-Za-z_][A-Za-z0-9_]*)\s*==\s*"(.*)"$/, trimmed) ->
        [_, key, expected] = match
        {:ok, to_string(context_get(context, key)) == expected}

      match = Regex.run(~r/^context\.([A-Za-z_][A-Za-z0-9_]*)\s*!=\s*'(.*)'$/, trimmed) ->
        [_, key, expected] = match
        {:ok, to_string(context_get(context, key)) != expected}

      match = Regex.run(~r/^context\.([A-Za-z_][A-Za-z0-9_]*)\s*!=\s*"(.*)"$/, trimmed) ->
        [_, key, expected] = match
        {:ok, to_string(context_get(context, key)) != expected}

      match = Regex.run(~r/^context\.([A-Za-z_][A-Za-z0-9_]*)$/, trimmed) ->
        [_, key] = match
        {:ok, truthy?(context_get(context, key))}

      true ->
        {:error, "unsupported conditional expression: #{inspect(trimmed)}"}
    end
  end

  def evaluate(_, _), do: {:error, "conditional when must be a string"}

  defp context_get(context, key) when is_map(context) and is_binary(key) do
    Map.get(context, key)
  end

  defp truthy?(nil), do: false
  defp truthy?(false), do: false
  defp truthy?(""), do: false
  defp truthy?(_), do: true

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
