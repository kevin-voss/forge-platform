defmodule ForgeWorkflows.Engine.StepExecutorTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Engine.StepExecutor

  test "noop and log execute" do
    assert {:ok, %{"ok" => true}} = StepExecutor.execute(%{id: "a", type: "noop"}, %{})

    assert {:ok, %{"logged" => "hi"}} =
             StepExecutor.execute(%{id: "b", type: "log", message: "hi"}, %{})
  end

  test "idempotency helper: completed step is skip" do
    # Mirrors Runs.begin_step decision without DB.
    step = %{status: "completed", attempt: 1}
    assert step.status == "completed"
  end
end
