defmodule ForgeWorkflows.Triggers.Registry do
  @moduledoc false

  alias ForgeWorkflows.Definitions.Workflow

  @spec rebuild!([Workflow.t()]) :: :ok
  def rebuild!(workflows) when is_list(workflows) do
    index =
      workflows
      |> Enum.filter(fn
        %Workflow{trigger: %{event: event}} when is_binary(event) and event != "" -> true
        _ -> false
      end)
      |> Enum.group_by(fn %Workflow{trigger: %{event: event}} -> event end)

    Application.put_env(:forge_workflows, :trigger_index, index)
    :ok
  end

  @spec workflows_for(String.t()) :: [Workflow.t()]
  def workflows_for(event_type) when is_binary(event_type) do
    Map.get(index(), event_type, [])
  end

  @spec match?(String.t()) :: boolean()
  def match?(event_type) when is_binary(event_type) do
    workflows_for(event_type) != []
  end

  @spec event_types() :: [String.t()]
  def event_types do
    index() |> Map.keys() |> Enum.sort()
  end

  @spec index() :: %{optional(String.t()) => [Workflow.t()]}
  def index do
    Application.get_env(:forge_workflows, :trigger_index, %{})
  end
end
