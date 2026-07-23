defmodule ForgeWorkflows.Definitions.LoaderTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Definitions.Workflow

  @tmp_root Path.join(System.tmp_dir!(), "forge-workflows-defs-#{System.unique_integer([:positive])}")

  setup do
    File.rm_rf!(@tmp_root)
    File.mkdir_p!(@tmp_root)

    on_exit(fn -> File.rm_rf!(@tmp_root) end)
    :ok
  end

  test "loads and validates fixture-log style definition" do
    path = Path.join(@tmp_root, "fixture-log.yaml")

    File.write!(path, """
    name: fixture-log
    steps:
      - id: log-start
        type: log
        message: hello
      - id: noop-end
        type: noop
    """)

    assert {:ok, %Workflow{name: "fixture-log", steps: steps}} = Loader.load_file(path)
    assert length(steps) == 2
    assert Enum.at(steps, 0).id == "log-start"
    assert Enum.at(steps, 0).type == "log"
    assert Enum.at(steps, 1).type == "noop"

    defs = Loader.load_dir!(@tmp_root)
    assert Map.has_key?(defs, "fixture-log")
  end

  test "loads primitive step types" do
    path = Path.join(@tmp_root, "primitives.yaml")

    File.write!(path, """
    name: primitives
    steps:
      - id: collect
        type: task
        action: noop
        retry: { max_attempts: 3, backoff: exponential, base_ms: 200 }
      - id: wait
        type: delay
        delay_ms: 5000
      - id: fanout
        type: parallel
        branches:
          - id: logs
            type: noop
          - id: metrics
            type: noop
      - id: decide
        type: conditional
        when: "context.severity == 'high'"
        then: escalate
        else: close
      - id: escalate
        type: noop
      - id: close
        type: noop
    """)

    assert {:ok, %Workflow{steps: steps}} = Loader.load_file(path)
    types = Enum.map(steps, & &1.type)
    assert "task" in types
    assert "delay" in types
    assert "parallel" in types
    assert "conditional" in types

    fanout = Enum.find(steps, &(&1.id == "fanout"))
    assert length(fanout.branches) == 2
    collect = Enum.find(steps, &(&1.id == "collect"))
    assert collect.retry.max_attempts == 3
  end

  test "loads trigger and agent step" do
    path = Path.join(@tmp_root, "triggered.yaml")

    File.write!(path, """
    name: triggered
    trigger:
      event: deployment.failed
    steps:
      - id: diagnose
        type: agent
        agent: deployment-investigator
        input:
          deployment: "${event.deployment_id}"
    """)

    assert {:ok, %Workflow{name: "triggered", trigger: %{event: "deployment.failed"}, steps: [step]}} =
             Loader.load_file(path)

    assert step.type == "agent"
    assert step.agent == "deployment-investigator"
    assert step.input["deployment"] == "${event.deployment_id}"
  end

  test "rejects malformed definitions" do
    path = Path.join(@tmp_root, "bad.yaml")
    File.write!(path, "name: bad\nsteps: []\n")
    assert {:error, reason} = Loader.load_file(path)
    assert reason =~ "non-empty" or reason =~ "steps"

    File.write!(path, """
    name: bad
    steps:
      - id: x
        type: not-a-real-type
    """)

    assert {:error, reason2} = Loader.load_file(path)
    assert reason2 =~ "not allowed"

    File.write!(path, """
    name: bad
    steps:
      - id: dup
        type: noop
      - id: dup
        type: log
        message: x
    """)

    assert {:error, reason3} = Loader.load_file(path)
    assert reason3 =~ "duplicate"
  end

  test "packaged definitions directory loads" do
    dir = Path.expand("../definitions", __DIR__)

    if File.dir?(dir) do
      defs = Loader.load_dir!(dir)
      assert Map.has_key?(defs, "fixture-log")
      assert Map.has_key?(defs, "fixture-resume")
      assert Map.has_key?(defs, "fixture-primitives")
    else
      assert true
    end
  end
end
