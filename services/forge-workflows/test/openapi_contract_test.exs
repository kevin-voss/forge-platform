defmodule ForgeWorkflows.OpenAPIContractTest do
  use ExUnit.Case, async: true

  @openapi_name "forge-workflows.openapi.yaml"

  test "OpenAPI declares three health/identity paths and no engine surface" do
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
      refute text =~ "/v1/workflows"
      refute text =~ "/v1/runs"
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
