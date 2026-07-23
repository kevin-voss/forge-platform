defmodule ForgeWorkflows.Health do
  @moduledoc false

  alias ForgeWorkflows.Repo

  @spec live() :: %{status: String.t()}
  def live, do: %{status: "live"}

  @spec ready() :: %{status: String.t()}
  def ready do
    cond do
      is_nil(Process.whereis(ForgeWorkflows.Supervisor)) ->
        %{status: "not_ready"}

      # Unit-test boots skip HTTP/Repo; readiness is OTP-only there.
      Application.get_env(:forge_workflows, :start_http, true) == false ->
        %{status: "ready"}

      not db_ready?() ->
        %{status: "not_ready"}

      true ->
        %{status: "ready"}
    end
  end


  @spec ready_status_code() :: 200 | 503
  def ready_status_code do
    case ready() do
      %{status: "ready"} -> 200
      _ -> 503
    end
  end

  defp db_ready? do
    case Repo.query("SELECT 1") do
      {:ok, _} -> true
      _ -> false
    end
  rescue
    _ -> false
  end
end
