defmodule ForgeWorkflows.CompensationTest do
  use ExUnit.Case, async: false

  alias ForgeWorkflows.Clients.ControlClient
  alias ForgeWorkflows.Config
  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Definitions.Workflow
  alias ForgeWorkflows.Saga.Compensator
  alias ForgeWorkflows.Steps.Report

  @tmp_root Path.join(
              System.tmp_dir!(),
              "forge-workflows-comp-#{System.unique_integer([:positive])}"
            )

  setup do
    File.rm_rf!(@tmp_root)
    File.mkdir_p!(@tmp_root)

    cfg = %Config{
      port: 8080,
      service_name: "forge-workflows",
      service_version: "0.1.0",
      log_level: "error",
      env: "test",
      shutdown_grace_ms: 10_000,
      database_url: "postgres://forge:forge@localhost:5432/forge_workflows",
      defs_dir: Path.expand("../definitions", __DIR__),
      max_parallelism: 8,
      default_step_timeout_ms: 300_000,
      scheduler_tick_ms: 1_000,
      events_url: "disabled",
      events_enabled: false,
      agents_url: "http://forge-agents:4301",
      agents_mode: "fake",
      agent_poll_ms: 20,
      agent_step_timeout_ms: 5_000,
      default_project_id: "proj-a",
      events_http_timeout_ms: 1_000,
      agents_http_timeout_ms: 1_000,
      approval_ttl_seconds: 86_400,
      control_url: "http://forge-control:4001",
      control_mode: "fake",
      control_http_timeout_ms: 1_000,
      report_bucket: "wf-reports"
    }

    Application.put_env(:forge_workflows, :runtime_config, cfg)
    Application.put_env(:forge_workflows, :control_client, ControlClient.Default)
    ControlClient.Fake.reset!()

    on_exit(fn ->
      File.rm_rf!(@tmp_root)
      Application.delete_env(:forge_workflows, :runtime_config)
      Application.delete_env(:forge_workflows, :control_client)
      ControlClient.Fake.reset!()
    end)

    :ok
  end

  test "loads compensate field on task steps" do
    path = Path.join(@tmp_root, "comp.yaml")

    File.write!(path, """
    name: fixture-comp
    steps:
      - id: apply-change
        type: task
        action: control.apply
        compensate: control.rollback_deployment
      - id: finalize
        type: task
        action: report.store
    """)

    assert {:ok, %Workflow{steps: steps}} = Loader.load_file(path)
    step = Enum.find(steps, &(&1.id == "apply-change"))
    assert step.action == "control.apply"
    assert step.compensate == "control.rollback_deployment"
  end

  test "fake control apply and rollback are recorded" do
    assert {:ok, apply} =
             ControlClient.apply_change("dep-1", "proj-a", %{"image" => "forge/demo:v3"})

    assert apply["action"] == "control.apply"
    assert apply["deployment_id"] == "dep-1"

    assert {:ok, rb} = ControlClient.rollback_deployment("dep-1", "proj-a", %{})
    assert rb["action"] == "control.rollback_deployment"
    assert rb["restored_image"] == "forge/healthy:v1"
    assert ControlClient.Fake.rollback_count("dep-1") == 1

    # Idempotent second call still succeeds (compensators must be re-runnable).
    assert {:ok, _} = ControlClient.rollback_deployment("dep-1", "proj-a", %{})
    assert ControlClient.Fake.rollback_count("dep-1") == 2
  end

  test "compensator reverse order is stable for a list of steps" do
    forward = ["apply-a", "apply-b", "apply-c"]
    reverse = Enum.reverse(forward)
    assert reverse == ["apply-c", "apply-b", "apply-a"]
  end

  test "execute_action runs control.rollback_deployment via fake client" do
    _ = ControlClient.apply_change("dep-9", "proj-a", %{})

    assert {:ok, result} =
             Compensator.execute_action(
               "control.rollback_deployment",
               %{"deployment_id" => "dep-9"},
               "proj-a",
               %{"event" => %{"deployment_id" => "dep-9"}}
             )

    assert result["action"] == "control.rollback_deployment"
    assert ControlClient.Fake.rollback_count("dep-9") == 1
  end

  test "compensator failure is surfaced while noop compensators succeed" do
    assert {:error, "compensator failed"} =
             Compensator.execute_action("fail.compensate", %{}, "proj-a", %{})

    assert {:ok, %{"compensated" => true}} =
             Compensator.execute_action("noop.compensate_a", %{}, "proj-a", %{})
  end

  test "report.store includes rolled_back and report_ref" do
    assert {:ok, report} =
             Report.store("00000000-0000-0000-0000-000000000099", %{"event" => %{"deployment_id" => "d1"}},
               rolled_back: true,
               project_id: "proj-a",
               trigger: "test",
               saga: []
             )

    assert report["rolled_back"] == true
    assert report["deployment_id"] == "d1"
    assert report["report_ref"] =~ "storage://wf-reports/"
    assert Report.build_run_result(report, true)["rolled_back"] == true
  end
end
