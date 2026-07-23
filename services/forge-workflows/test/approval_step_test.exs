defmodule ForgeWorkflows.ApprovalStepTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Definitions.Workflow
  alias ForgeWorkflows.Steps.Approval

  @tmp_root Path.join(System.tmp_dir!(), "forge-workflows-approval-#{System.unique_integer([:positive])}")

  setup do
    File.rm_rf!(@tmp_root)
    File.mkdir_p!(@tmp_root)
    on_exit(fn -> File.rm_rf!(@tmp_root) end)
    :ok
  end

  test "loads approval step with prompt and on_deny" do
    path = Path.join(@tmp_root, "approval.yaml")

    File.write!(path, """
    name: fixture-approval
    steps:
      - id: approve-rollback
        type: approval
        prompt: "Approve rollback of ${event.deployment_id}?"
        on_deny: close
      - id: close
        type: log
        message: denied
    """)

    assert {:ok, %Workflow{steps: steps}} = Loader.load_file(path)
    step = Enum.find(steps, &(&1.id == "approve-rollback"))
    assert step.type == "approval"
    assert step.prompt =~ "Approve rollback"
    assert step.on_deny == "close"
  end

  test "rejects approval without prompt" do
    path = Path.join(@tmp_root, "bad.yaml")

    File.write!(path, """
    name: bad
    steps:
      - id: a
        type: approval
    """)

    assert {:error, reason} = Loader.load_file(path)
    assert reason =~ "prompt"
  end

  test "rejects unknown on_deny target" do
    path = Path.join(@tmp_root, "bad-deny.yaml")

    File.write!(path, """
    name: bad-deny
    steps:
      - id: a
        type: approval
        prompt: "ok?"
        on_deny: missing
    """)

    assert {:error, reason} = Loader.load_file(path)
    assert reason =~ "on_deny"
  end

  test "resolves prompt templates from run input" do
    prompt =
      Approval.resolve_prompt("Approve rollback of ${event.deployment_id}?", %{
        "event" => %{"deployment_id" => "dep-42"}
      })

    assert prompt == "Approve rollback of dep-42?"
  end
end
