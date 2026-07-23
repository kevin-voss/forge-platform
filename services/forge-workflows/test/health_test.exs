defmodule ForgeWorkflows.HealthTest do
  use ExUnit.Case, async: true

  alias ForgeWorkflows.Health

  test "live returns live status" do
    assert Health.live() == %{status: "live"}
  end

  test "ready returns ready when OTP supervisor is up" do
    assert is_pid(Process.whereis(ForgeWorkflows.Supervisor))
    assert Health.ready() == %{status: "ready"}
    assert Health.ready_status_code() == 200
  end
end
