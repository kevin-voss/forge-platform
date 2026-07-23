defmodule ForgeWorkflows.Health do
  @moduledoc false

  @spec live() :: %{status: String.t()}
  def live, do: %{status: "live"}

  @spec ready() :: %{status: String.t()}
  def ready do
    # 16.01 gates on OTP app start only. DB reachability is added in 16.02.
    if Process.whereis(ForgeWorkflows.Supervisor) do
      %{status: "ready"}
    else
      %{status: "not_ready"}
    end
  end

  @spec ready_status_code() :: 200 | 503
  def ready_status_code do
    case ready() do
      %{status: "ready"} -> 200
      _ -> 503
    end
  end
end
