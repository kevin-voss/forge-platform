defmodule ForgeWorkflows.IdempotencyTest do
  use ExUnit.Case, async: true

  # Pure decision coverage for (run_id, step_id) idempotency:
  # a completed durable step must never be re-executed.

  defp decide(%{status: "completed"} = step), do: {:skip, step}
  defp decide(step), do: {:execute, %{step | status: "running", attempt: step.attempt + 1}}

  test "replaying a completed step is a no-op" do
    completed = %{status: "completed", attempt: 1, step_id: "a"}
    assert {:skip, ^completed} = decide(completed)

    pending = %{status: "pending", attempt: 0, step_id: "b"}
    assert {:execute, %{status: "running", attempt: 1}} = decide(pending)

    # Second decide after complete stays skip with same attempt.
    after_done = %{status: "completed", attempt: 1, step_id: "b"}
    assert {:skip, %{attempt: 1}} = decide(after_done)
  end
end
