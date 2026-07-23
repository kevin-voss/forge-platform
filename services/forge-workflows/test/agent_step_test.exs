defmodule ForgeWorkflows.AgentStepTest do
  use ExUnit.Case, async: false

  alias ForgeWorkflows.Clients.AgentClient
  alias ForgeWorkflows.Config
  alias ForgeWorkflows.Steps.Agent
  alias ForgeWorkflows.Steps.Retry

  setup do
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
      approval_ttl_seconds: 86_400
    }

    Application.put_env(:forge_workflows, :runtime_config, cfg)
    Application.put_env(:forge_workflows, :agent_client, AgentClient.Default)

    on_exit(fn ->
      Application.delete_env(:forge_workflows, :runtime_config)
      Application.delete_env(:forge_workflows, :agent_client)
    end)

    :ok
  end

  test "resolves ${event.*} input templates" do
    assert {:ok, %{"deployment" => "dep-123"}} =
             Agent.resolve_input(%{"deployment" => "${event.deployment_id}"}, %{
               "event" => %{"deployment_id" => "dep-123"}
             })
  end

  test "agent step maps run result into context" do
    step = %{
      id: "diagnose",
      type: "agent",
      agent: "deployment-investigator",
      input: %{"deployment" => "${event.deployment_id}"}
    }

    input = %{"event" => %{"deployment_id" => "dep-9"}, "project_id" => "proj-a"}

    assert {:ok, output} = Agent.execute(step, input, project_id: "proj-a", poll_ms: 10)
    assert output["status"] == "succeeded"
    assert output["agent"] == "deployment-investigator"
    assert is_binary(output["agent_run_id"])
    assert output["input"]["deployment"] == "dep-9"
    assert is_binary(output["result"])
    assert output["result"] =~ "dep-9"
  end

  test "agent awaiting_approval is surfaced" do
    put_mode("awaiting")

    step = %{
      id: "diagnose",
      type: "agent",
      agent: "deployment-investigator",
      input: %{"deployment" => "dep-1"}
    }

    assert {:ok, output} =
             Agent.execute(step, %{}, project_id: "proj-a", poll_ms: 10, timeout_ms: 2_000)

    assert output["status"] == "awaiting_approval"
    assert output["awaiting_approval"] == true
    assert is_map(output["pending_approval"])
  end

  test "agent unavailable retries then fails per policy" do
    put_mode("fail")

    step = %{
      id: "diagnose",
      type: "agent",
      agent: "deployment-investigator",
      input: %{},
      retry: %{max_attempts: 3, backoff: "fixed", base_ms: 1}
    }

    # Simulate engine retry scheduling decisions.
    assert Retry.should_retry?(step.retry, 1)
    assert Retry.should_retry?(step.retry, 2)
    refute Retry.should_retry?(step.retry, 3)

    assert {:error, "agent unavailable"} =
             Agent.execute(step, %{}, project_id: "proj-a", poll_ms: 5, timeout_ms: 500)

    assert :exhausted = Retry.schedule(step.retry, 3)
  end

  defp put_mode(mode) do
    cfg = Application.fetch_env!(:forge_workflows, :runtime_config)
    Application.put_env(:forge_workflows, :runtime_config, %{cfg | agents_mode: mode})
  end
end
