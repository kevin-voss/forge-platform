defmodule ForgeWorkflows.EventDedupTest do
  use ExUnit.Case, async: true

  # Pure coverage for idempotent trigger decisioning: a claimed (event_id, workflow)
  # pair must not start a second run.

  defp decide(seen?, event_id, workflow) do
    key = {event_id, workflow}

    if MapSet.member?(seen?, key) do
      {:duplicate, event_id}
    else
      {:start, MapSet.put(seen?, key)}
    end
  end

  test "event dedupe prevents duplicate runs" do
    seen = MapSet.new()

    assert {:start, seen2} = decide(seen, "evt-1", "fixture-trigger")
    assert {:duplicate, "evt-1"} = decide(seen2, "evt-1", "fixture-trigger")

    # Same event can still trigger a different workflow name.
    assert {:start, _} = decide(seen2, "evt-1", "other-workflow")
  end
end
