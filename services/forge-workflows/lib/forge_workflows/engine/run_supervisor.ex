defmodule ForgeWorkflows.Engine.RunSupervisor do
  @moduledoc false

  use DynamicSupervisor

  alias ForgeWorkflows.Engine.RunServer

  def start_link(opts \\ []) do
    DynamicSupervisor.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    DynamicSupervisor.init(strategy: :one_for_one)
  end

  @spec start_run(String.t()) :: {:ok, pid()} | {:error, term()}
  def start_run(run_id) when is_binary(run_id) do
    spec = {RunServer, run_id}

    case DynamicSupervisor.start_child(__MODULE__, spec) do
      {:ok, pid} -> {:ok, pid}
      {:error, {:already_started, pid}} -> {:ok, pid}
      other -> other
    end
  end
end
