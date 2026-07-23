defmodule ForgeWorkflows.Engine.RunServer do
  @moduledoc false

  use GenServer, restart: :transient

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Engine.StepExecutor
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Runs

  def start_link(run_id) when is_binary(run_id) do
    GenServer.start_link(__MODULE__, run_id, name: via(run_id))
  end

  def via(run_id), do: {:via, Registry, {ForgeWorkflows.RunRegistry, run_id}}

  @impl true
  def init(run_id) do
    Process.flag(:trap_exit, true)
    {:ok, %{run_id: run_id}, {:continue, :drive}}
  end

  @impl true
  def handle_continue(:drive, %{run_id: run_id} = state) do
    case drive(run_id) do
      :ok -> {:stop, :normal, state}
      {:error, reason} -> {:stop, reason, state}
    end
  end

  @impl true
  def terminate(_reason, _state), do: :ok

  defp drive(run_id) do
    case Runs.get_run_record(run_id) do
      nil ->
        log("run missing", run_id, %{})
        {:error, :not_found}

      %{status: status} when status in ["completed", "failed"] ->
        :ok

      run ->
        case Loader.get(run.workflow) do
          nil ->
            _ = Runs.mark_run_failed(run_id, "workflow definition not found: #{run.workflow}")
            {:error, :workflow_not_found}

          workflow ->
            execute_steps(run_id, workflow.steps, run.input || %{})
        end
    end
  end

  defp execute_steps(run_id, steps, input) do
    Enum.reduce_while(steps, :ok, fn step_def, :ok ->
      step_id = step_def.id

      case Runs.begin_step(run_id, step_id) do
        {:ok, :skip, step} ->
          log("skipping completed step", run_id, %{
            step_id: step_id,
            status: step.status,
            attempt: step.attempt
          })

          {:cont, :ok}

        {:ok, :execute, _step} ->
          _ = Runs.mark_run_running(run_id, step_id)

          log("step starting", run_id, %{step_id: step_id, type: step_def.type})

          case StepExecutor.execute(step_def, input) do
            {:ok, output} ->
              case Runs.complete_step(run_id, step_id, output) do
                {:ok, _} ->
                  log("step completed", run_id, %{step_id: step_id, status: "completed"})
                  {:cont, :ok}

                {:error, reason} ->
                  _ = Runs.mark_run_failed(run_id, "failed to persist step: #{inspect(reason)}")
                  {:halt, {:error, reason}}
              end

            {:error, reason} ->
              _ = Runs.fail_step(run_id, step_id, reason)
              _ = Runs.mark_run_failed(run_id, reason)
              log("step failed", run_id, %{step_id: step_id, status: "failed", error: reason})
              {:halt, {:error, reason}}
          end

        {:error, reason} ->
          _ = Runs.mark_run_failed(run_id, "begin_step failed: #{inspect(reason)}")
          {:halt, {:error, reason}}
      end
    end)
    |> case do
      :ok ->
        result = %{"ok" => true}
        _ = Runs.mark_run_completed(run_id, result)
        log("run completed", run_id, %{status: "completed"})
        :ok

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp log(message, run_id, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, Map.merge(%{run_id: run_id}, fields))
  end
end
