defmodule ForgeWorkflows.Definitions.Loader do
  @moduledoc false

  alias ForgeWorkflows.Definitions.Workflow
  alias ForgeWorkflows.Triggers.Registry

  @spec load_dir!(String.t()) :: %{optional(String.t()) => Workflow.t()}
  def load_dir!(dir) when is_binary(dir) do
    unless File.dir?(dir) do
      raise ArgumentError, "definitions directory does not exist: #{dir}"
    end

    dir
    |> Path.join("*.yaml")
    |> Path.wildcard()
    |> Kernel.++(Path.wildcard(Path.join(dir, "*.yml")))
    |> Enum.sort()
    |> Enum.reduce(%{}, fn path, acc ->
      case load_file(path) do
        {:ok, workflow} ->
          if Map.has_key?(acc, workflow.name) do
            raise ArgumentError, "duplicate workflow name #{inspect(workflow.name)} in #{path}"
          end

          Map.put(acc, workflow.name, workflow)

        {:error, reason} ->
          raise ArgumentError, "invalid workflow definition #{path}: #{reason}"
      end
    end)
  end

  @spec load_file(String.t()) :: {:ok, Workflow.t()} | {:error, String.t()}
  def load_file(path) when is_binary(path) do
    with {:ok, contents} <- File.read(path),
         {:ok, raw} <- decode_yaml(contents),
         {:ok, workflow} <- Workflow.from_map(raw) do
      {:ok, workflow}
    else
      {:error, reason} when is_binary(reason) -> {:error, reason}
      {:error, reason} -> {:error, Exception.message(reason)}
    end
  end

  @spec get(String.t()) :: Workflow.t() | nil
  def get(name) when is_binary(name) do
    Map.get(definitions(), name)
  end

  @spec list() :: [Workflow.t()]
  def list do
    definitions()
    |> Map.values()
    |> Enum.sort_by(& &1.name)
  end

  @spec put_definitions(%{optional(String.t()) => Workflow.t()}) :: :ok
  def put_definitions(map) when is_map(map) do
    Application.put_env(:forge_workflows, :definitions, map)
    Registry.rebuild!(Map.values(map))
    :ok
  end

  defp definitions do
    Application.get_env(:forge_workflows, :definitions, %{})
  end

  defp decode_yaml(contents) do
    case YamlElixir.read_from_string(contents) do
      {:ok, data} when is_map(data) -> {:ok, stringify_keys(data)}
      {:ok, _} -> {:error, "YAML root must be a mapping"}
      {:error, reason} -> {:error, Exception.message(reason)}
    end
  end

  defp stringify_keys(map) when is_map(map) do
    Map.new(map, fn
      {k, v} when is_atom(k) -> {Atom.to_string(k), stringify_keys(v)}
      {k, v} when is_binary(k) -> {k, stringify_keys(v)}
      {k, v} -> {to_string(k), stringify_keys(v)}
    end)
  end

  defp stringify_keys(list) when is_list(list), do: Enum.map(list, &stringify_keys/1)
  defp stringify_keys(other), do: other
end
