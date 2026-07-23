defmodule ForgeWorkflows.StepPrimitivesTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Steps.Conditional
  alias ForgeWorkflows.Steps.Parallel
  alias ForgeWorkflows.Steps.Retry
  alias ForgeWorkflows.Steps.Timeout

  describe "retry" do
    test "backoff schedule fixed and exponential" do
      fixed = %{max_attempts: 3, backoff: "fixed", base_ms: 200}
      assert Retry.backoff_ms(fixed, 1) == 200
      assert Retry.backoff_ms(fixed, 2) == 200

      exp = %{max_attempts: 4, backoff: "exponential", base_ms: 100}
      assert Retry.backoff_ms(exp, 1) == 100
      assert Retry.backoff_ms(exp, 2) == 200
      assert Retry.backoff_ms(exp, 3) == 400
    end

    test "attempt cap" do
      policy = %{max_attempts: 3, backoff: "fixed", base_ms: 10}
      assert Retry.should_retry?(policy, 1)
      assert Retry.should_retry?(policy, 2)
      refute Retry.should_retry?(policy, 3)
      assert {:retry, 10} = Retry.schedule(policy, 1)
      assert :exhausted = Retry.schedule(policy, 3)
    end
  end

  describe "conditional" do
    test "true and false branch selection" do
      step = %{
        id: "decide",
        type: "conditional",
        when: "context.severity == 'high'",
        then: "escalate",
        else: "close"
      }

      assert {:ok, "escalate", true} = Conditional.select(step, %{"severity" => "high"})
      assert {:ok, "close", false} = Conditional.select(step, %{"severity" => "low"})
    end

    test "rejects unsafe expressions" do
      assert {:error, _} = Conditional.evaluate("System.cmd('id')", %{})
      assert {:error, _} = Conditional.evaluate("", %{})
    end
  end

  describe "parallel" do
    test "join aggregates child results" do
      assert {:ok, %{"branches" => branches, "policy" => "collect"}} =
               Parallel.join_results([
                 {"logs", {:ok, %{"ok" => true}}},
                 {"metrics", {:ok, %{"n" => 1}}}
               ])

      assert branches["logs"]["status"] == "completed"
      assert branches["metrics"]["output"]["n"] == 1
    end

    test "join fails when any branch failed" do
      assert {:error, reason} =
               Parallel.join_results([
                 {"logs", {:ok, %{}}},
                 {"metrics", {:error, "boom"}}
               ])

      assert reason =~ "metrics"
    end
  end

  describe "timeout" do
    test "marks step failed on deadline" do
      assert {:error, "timeout"} =
               Timeout.execute_with_timeout(
                 fn ->
                   Process.sleep(200)
                   {:ok, %{}}
                 end,
                 20
               )
    end

    test "returns result when within deadline" do
      assert {:ok, %{"ok" => true}} =
               Timeout.execute_with_timeout(fn -> {:ok, %{"ok" => true}} end, 500)
    end

    test "run_expired?" do
      started = DateTime.utc_now() |> DateTime.add(-10, :second)
      assert Timeout.run_expired?(started, 1_000, DateTime.utc_now())
      refute Timeout.run_expired?(started, 60_000, DateTime.utc_now())
      refute Timeout.run_expired?(started, nil, DateTime.utc_now())
    end
  end
end
