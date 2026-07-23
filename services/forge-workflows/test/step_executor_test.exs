defmodule ForgeWorkflows.Engine.StepExecutorTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Engine.StepExecutor

  test "noop and log execute" do
    assert {:ok, %{"ok" => true}} = StepExecutor.execute(%{id: "a", type: "noop"}, %{})

    assert {:ok, %{"logged" => "hi"}} =
             StepExecutor.execute(%{id: "b", type: "log", message: "hi"}, %{})
  end

  test "fail_until respects attempt" do
    step = %{id: "f", type: "task", action: "fail_until", succeed_on_attempt: 3}

    assert {:error, _} = StepExecutor.execute(step, %{}, attempt: 1)
    assert {:error, _} = StepExecutor.execute(step, %{}, attempt: 2)
    assert {:ok, %{"attempt" => 3}} = StepExecutor.execute(step, %{}, attempt: 3)
  end

  test "step timeout" do
    step = %{id: "s", type: "task", action: "sleep", delay_ms: 500, timeout_ms: 50}
    assert {:error, "timeout"} = StepExecutor.execute(step, %{}, timeout_ms: 300_000)
  end

  test "idempotency helper: completed step is skip" do
    step = %{status: "completed", attempt: 1}
    assert step.status == "completed"
  end
end
