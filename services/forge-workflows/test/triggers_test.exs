defmodule ForgeWorkflows.TriggersTest do
  use ExUnit.Case, async: false

  alias ForgeWorkflows.Definitions.Workflow
  alias ForgeWorkflows.Triggers.Registry

  setup do
    workflows = [
      %Workflow{
        name: "on-fail",
        trigger: %{event: "deployment.failed"},
        steps: [%{id: "a", type: "noop"}]
      },
      %Workflow{
        name: "on-fail-b",
        trigger: %{event: "deployment.failed"},
        steps: [%{id: "a", type: "noop"}]
      },
      %Workflow{
        name: "on-complete",
        trigger: %{event: "deployment.completed"},
        steps: [%{id: "a", type: "noop"}]
      },
      %Workflow{
        name: "manual",
        trigger: nil,
        steps: [%{id: "a", type: "noop"}]
      }
    ]

    Registry.rebuild!(workflows)

    on_exit(fn -> Registry.rebuild!([]) end)
    :ok
  end

  test "trigger matching by event type" do
    matched = Registry.workflows_for("deployment.failed")
    assert length(matched) == 2
    assert Enum.map(matched, & &1.name) |> Enum.sort() == ["on-fail", "on-fail-b"]

    assert Registry.match?("deployment.failed")
    refute Registry.match?("runtime.crashed")
    assert Registry.workflows_for("runtime.crashed") == []

    assert Registry.event_types() == ["deployment.completed", "deployment.failed"]
  end
end
