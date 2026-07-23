defmodule ForgeWorkflows.OpenAPIContractTest do
  use ExUnit.Case, async: true

  @openapi_name "forge-workflows.openapi.yaml"

  test "OpenAPI declares health, workflows, and run endpoints" do
    path = resolve_openapi()

    if path == nil do
      # Docker build context does not include contracts/; integration tests cover it.
      assert true
    else
      text = File.read!(path)
      assert text =~ "openapi:"
      assert text =~ "/health/live"
      assert text =~ "/health/ready"
      assert text =~ "\n  /:"
      assert text =~ "forge-workflows"
      assert text =~ "elixir"
      assert text =~ "/v1/workflows"
      assert text =~ "/v1/runs"
      assert text =~ "WorkflowRun"
      assert text =~ "WorkflowStep"
      assert text =~ "wake_at"
      assert text =~ "parent_step_id"
      assert text =~ "parallel"
      assert text =~ "conditional"
      assert text =~ "RetryPolicy"
      assert text =~ "/v1/triggers/test"
      assert text =~ "deployment.failed"
      assert text =~ "agent"
      assert text =~ "TriggerConfig"
      assert text =~ "approval"
      assert text =~ "/v1/approvals"
      assert text =~ "awaiting_approval"
      assert text =~ "WorkflowApproval"
    end
  end

  defp resolve_openapi do
    here = Path.expand(__DIR__)

    candidates = [
      Path.expand("../../contracts/openapi/#{@openapi_name}", here),
      Path.expand("../../../contracts/openapi/#{@openapi_name}", here)
    ]

    Enum.find(candidates, &File.regular?/1)
  end
end
