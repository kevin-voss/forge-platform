defmodule ForgeWorkflows.Engine.RunServer do
  @moduledoc false

  use GenServer, restart: :transient

  alias ForgeWorkflows.Approvals.Store, as: ApprovalStore
  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Engine.StepExecutor
  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Runs
  alias ForgeWorkflows.Steps.Approval
  alias ForgeWorkflows.Steps.Conditional
  alias ForgeWorkflows.Steps.Delay
  alias ForgeWorkflows.Steps.Parallel
  alias ForgeWorkflows.Steps.Retry
  alias ForgeWorkflows.Steps.Timeout

  def start_link(run_id) when is_binary(run_id) do
    GenServer.start_link(__MODULE__, run_id, name: via(run_id))
  end

  def via(run_id), do: {:via, Registry, {ForgeWorkflows.RunRegistry, run_id}}

  @doc """
  Wake a run that is waiting on a durable timer (delay/retry) or human approval.
  """
  @spec wake(String.t(), String.t()) :: :ok
  def wake(run_id, step_id) when is_binary(run_id) and is_binary(step_id) do
    case Registry.lookup(ForgeWorkflows.RunRegistry, run_id) do
      [{pid, _}] ->
        send(pid, {:timer_due, step_id})
        :ok

      [] ->
        _ = ForgeWorkflows.Engine.RunSupervisor.start_run(run_id)
        :ok
    end
  end

  @impl true
  def init(run_id) do
    Process.flag(:trap_exit, true)
    {:ok, %{run_id: run_id}, {:continue, :drive}}
  end

  @impl true
  def handle_continue(:drive, state) do
    case drive_once(state.run_id) do
      :done ->
        {:stop, :normal, state}

      :waiting ->
        {:noreply, state}

      {:error, reason} ->
        {:stop, reason, state}
    end
  end

  @impl true
  def handle_info({:timer_due, _step_id}, state) do
    {:noreply, state, {:continue, :drive}}
  end

  def handle_info(_msg, state), do: {:noreply, state}

  @impl true
  def terminate(_reason, _state), do: :ok

  defp drive_once(run_id) do
    case Runs.get_run_record(run_id) do
      nil ->
        log("run missing", run_id, %{})
        {:error, :not_found}

      %{status: status} when status in ["completed", "failed"] ->
        :done

      run ->
        case Loader.get(run.workflow) do
          nil ->
            _ = Runs.mark_run_failed(run_id, "workflow definition not found: #{run.workflow}")
            {:error, :workflow_not_found}

          workflow ->
            cfg = runtime_limits()
            now = DateTime.utc_now()

            if Timeout.run_expired?(run.inserted_at, workflow.timeout_ms, now) do
              Metrics.inc_timeout()
              _ = Runs.mark_run_failed(run_id, "timeout")
              log("run timeout", run_id, %{timeout_ms: workflow.timeout_ms})
              {:error, :timeout}
            else
              advance(run_id, workflow, run.input || %{}, run.project_id, cfg)
            end
        end
    end
  end

  defp advance(run_id, workflow, input, project_id, cfg) do
    steps = workflow.steps
    by_id = Map.new(steps, &{&1.id, &1})
    branch_owned = parallel_ref_ids(steps)

    Enum.reduce_while(steps, :continue, fn step_def, :continue ->
      cond do
        MapSet.member?(branch_owned, step_def.id) ->
          # Executed only as a parallel child.
          {:cont, :continue}

        true ->
          handle_step(run_id, step_def, by_id, input, project_id, cfg)
      end
    end)
    |> case do
      :continue ->
        result = %{"ok" => true}
        _ = Runs.mark_run_completed(run_id, result)
        log("run completed", run_id, %{status: "completed"})
        :done

      :waiting ->
        :waiting

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp handle_step(run_id, step_def, by_id, input, project_id, cfg) do
    step_id = step_def.id

    case Runs.begin_step(run_id, step_id) do
      {:ok, :skip, step} ->
        log("skipping completed step", run_id, %{
          step_id: step_id,
          status: step.status,
          attempt: step.attempt
        })

        {:cont, :continue}

      {:ok, :wake, step} ->
        handle_wake(run_id, step_def, step, by_id, input, project_id, cfg)

      {:ok, :execute, step} ->
        _ = Runs.mark_run_running(run_id, step_id)
        log("step starting", run_id, %{step_id: step_id, type: step_def.type, attempt: step.attempt})
        dispatch(run_id, step_def, step, by_id, input, project_id, cfg)

      {:error, reason} ->
        _ = Runs.mark_run_failed(run_id, "begin_step failed: #{inspect(reason)}")
        {:halt, {:error, reason}}
    end
  end

  defp handle_wake(run_id, step_def, step, by_id, input, project_id, cfg) do
    now = DateTime.utc_now()

    case step_def.type do
      "delay" ->
        if Delay.due?(step.wake_at, now) do
          case Delay.complete(run_id, step_id(step_def), step_def.delay_ms) do
            {:ok, _} -> {:cont, :continue}
            {:error, reason} -> {:halt, {:error, reason}}
          end
        else
          arm_local_timer(step.wake_at, now, step_id(step_def))
          {:halt, :waiting}
        end

      "approval" ->
        handle_approval_wake(run_id, step_def, step, now)

      _ ->
        # Retry backoff wake — re-enter execute path with incremented attempt.
        case Runs.rebegin_after_wait(run_id, step_id(step_def)) do
          {:ok, %ForgeWorkflows.Schemas.Step{status: "completed"}} ->
            {:cont, :continue}

          {:ok, stepped} ->
            _ = Runs.mark_run_running(run_id, step_id(step_def))

            log("retry wake", run_id, %{
              step_id: step_id(step_def),
              attempt: stepped.attempt
            })

            dispatch(run_id, step_def, stepped, by_id, input, project_id, cfg)

          {:error, reason} ->
            _ = Runs.mark_run_failed(run_id, "rebegin failed: #{inspect(reason)}")
            {:halt, {:error, reason}}
        end
    end
  end

  defp handle_approval_wake(run_id, step_def, step, now) do
    case ApprovalStore.get_by_run_step(run_id, step_id(step_def)) do
      nil ->
        _ = Runs.mark_run_failed(run_id, "approval missing for step #{step_id(step_def)}")
        {:halt, {:error, :approval_missing}}

      %{status: "pending"} = approval ->
        if Delay.due?(approval.expires_at, now) do
          case ApprovalStore.expire(approval) do
            {:ok, expired} ->
              workflow_steps = workflow_steps_for_run(run_id)
              Approval.apply_decision(run_id, step_def, expired, workflow_steps)

            {:error, reason} ->
              _ = Runs.mark_run_failed(run_id, "approval expire failed: #{inspect(reason)}")
              {:halt, {:error, reason}}
          end
        else
          _ = Runs.mark_run_awaiting_approval(run_id, step_id(step_def))

          if step.wake_at do
            arm_local_timer(step.wake_at, now, step_id(step_def))
          end

          log("approval still pending; parked", run_id, %{
            step_id: step_id(step_def),
            approval_id: approval.id
          })

          {:halt, :waiting}
        end

      approval ->
        workflow_steps = workflow_steps_for_run(run_id)
        Approval.apply_decision(run_id, step_def, approval, workflow_steps)
    end
  end

  defp dispatch(run_id, step_def, step, by_id, input, project_id, cfg) do
    case step_def.type do
      "delay" ->
        case Delay.schedule(run_id, step_def.id, step_def.delay_ms) do
          {:ok, wake_at} ->
            arm_local_timer(wake_at, DateTime.utc_now(), step_def.id)
            {:halt, :waiting}

          {:error, reason} ->
            _ = Runs.mark_run_failed(run_id, "delay schedule failed: #{inspect(reason)}")
            {:halt, {:error, reason}}
        end

      "approval" ->
        ttl = Map.get(cfg, :approval_ttl_seconds, 86_400)

        case Approval.park(run_id, step_def, project_id, input, ttl) do
          {:ok, approval} ->
            if approval.expires_at do
              arm_local_timer(approval.expires_at, DateTime.utc_now(), step_def.id)
            end

            {:halt, :waiting}

          {:error, reason} ->
            _ = Runs.mark_run_failed(run_id, "approval park failed: #{inspect(reason)}")
            {:halt, {:error, reason}}
        end

      "conditional" ->
        case Conditional.select(step_def, input) do
          {:ok, branch, truthy} ->
            other = if branch == step_def.then, do: step_def.else, else: step_def.then
            _ = Runs.skip_step(run_id, other, %{"skipped" => true, "reason" => "not_selected"})

            output = %{"branch" => branch, "when" => truthy}

            case Runs.complete_step(run_id, step_def.id, output) do
              {:ok, _} -> {:cont, :continue}
              {:error, reason} -> {:halt, {:error, reason}}
            end

          {:error, reason} ->
            _ = Runs.fail_step(run_id, step_def.id, reason)
            _ = Runs.mark_run_failed(run_id, reason)
            {:halt, {:error, reason}}
        end

      "parallel" ->
        branches = resolve_branches(step_def, by_id)

        case Parallel.run(run_id, step_def.id, branches, input,
               max_parallelism: cfg.max_parallelism,
               default_step_timeout_ms: cfg.default_step_timeout_ms
             ) do
          {:ok, output} ->
            case Runs.complete_step(run_id, step_def.id, output) do
              {:ok, _} ->
                # Mark string-ref children already completed inside Parallel; skip none.
                {:cont, :continue}

              {:error, reason} ->
                {:halt, {:error, reason}}
            end

          {:error, reason} ->
            _ = Runs.fail_step(run_id, step_def.id, reason)
            _ = Runs.mark_run_failed(run_id, reason)
            log("step failed", run_id, %{step_id: step_def.id, status: "failed", error: reason})
            {:halt, {:error, reason}}
        end

      _ ->
        execute_actionable(run_id, step_def, step, input, project_id, cfg)
    end
  end

  defp execute_actionable(run_id, step_def, step, input, project_id, cfg) do
    case StepExecutor.execute(step_def, input,
           attempt: step.attempt,
           timeout_ms: cfg.default_step_timeout_ms,
           project_id: project_id,
           agent_poll_ms: cfg.agent_poll_ms,
           agent_step_timeout_ms: cfg.agent_step_timeout_ms
         ) do
      {:ok, output} ->
        case Runs.complete_step(run_id, step_def.id, output) do
          {:ok, _} ->
            log("step completed", run_id, %{step_id: step_def.id, status: "completed"})
            {:cont, :continue}

          {:error, reason} ->
            _ = Runs.mark_run_failed(run_id, "failed to persist step: #{inspect(reason)}")
            {:halt, {:error, reason}}
        end

      {:error, reason} ->
        handle_failure(run_id, step_def, step, reason)
    end
  end

  defp handle_failure(run_id, step_def, step, reason) do
    policy = Map.get(step_def, :retry)

    case policy && Retry.schedule(policy, step.attempt) do
      {:retry, backoff_ms} ->
        Metrics.inc_retry()
        now = DateTime.utc_now() |> DateTime.truncate(:microsecond)
        wake_at = DateTime.add(now, backoff_ms, :millisecond)

        log("step retry scheduled", run_id, %{
          step_id: step_def.id,
          attempt: step.attempt,
          backoff_ms: backoff_ms,
          error: reason
        })

        case Runs.mark_step_waiting(run_id, step_def.id, wake_at) do
          {:ok, _} ->
            arm_local_timer(wake_at, now, step_def.id)
            {:halt, :waiting}

          {:error, err} ->
            _ = Runs.mark_run_failed(run_id, "retry schedule failed: #{inspect(err)}")
            {:halt, {:error, err}}
        end

      _ ->
        fail_reason = if reason == "timeout", do: "timeout", else: reason
        _ = Runs.fail_step(run_id, step_def.id, fail_reason)
        _ = Runs.mark_run_failed(run_id, fail_reason)
        log("step failed", run_id, %{step_id: step_def.id, status: "failed", error: fail_reason})
        {:halt, {:error, fail_reason}}
    end
  end

  defp resolve_branches(step_def, by_id) do
    Enum.map(step_def.branches || [], fn
      %{type: "ref", id: id} -> Map.fetch!(by_id, id)
      branch -> branch
    end)
  end

  defp parallel_ref_ids(steps) do
    steps
    |> Enum.filter(&(&1.type == "parallel"))
    |> Enum.flat_map(fn p ->
      Enum.map(p.branches || [], fn
        %{type: "ref", id: id} -> id
        _ -> nil
      end)
    end)
    |> Enum.reject(&is_nil/1)
    |> MapSet.new()
  end

  defp arm_local_timer(wake_at, now, step_id) do
    ms = Delay.remaining_ms(wake_at, now)
    Process.send_after(self(), {:timer_due, step_id}, max(ms, 1))
  end

  defp step_id(step_def), do: step_def.id

  defp workflow_steps_for_run(run_id) do
    case Runs.get_run_record(run_id) do
      %{workflow: name} ->
        case Loader.get(name) do
          %{steps: steps} -> steps
          _ -> []
        end

      _ ->
        []
    end
  end

  defp runtime_limits do
    case Application.get_env(:forge_workflows, :runtime_config) do
      %{
        max_parallelism: max,
        default_step_timeout_ms: timeout,
        agent_poll_ms: poll,
        agent_step_timeout_ms: agent_timeout,
        approval_ttl_seconds: ttl
      } ->
        %{
          max_parallelism: max,
          default_step_timeout_ms: timeout,
          agent_poll_ms: poll,
          agent_step_timeout_ms: agent_timeout,
          approval_ttl_seconds: ttl
        }

      %{
        max_parallelism: max,
        default_step_timeout_ms: timeout,
        agent_poll_ms: poll,
        agent_step_timeout_ms: agent_timeout
      } ->
        %{
          max_parallelism: max,
          default_step_timeout_ms: timeout,
          agent_poll_ms: poll,
          agent_step_timeout_ms: agent_timeout,
          approval_ttl_seconds: 86_400
        }

      %{max_parallelism: max, default_step_timeout_ms: timeout} ->
        %{
          max_parallelism: max,
          default_step_timeout_ms: timeout,
          agent_poll_ms: 1_000,
          agent_step_timeout_ms: 300_000,
          approval_ttl_seconds: 86_400
        }

      _ ->
        %{
          max_parallelism: 8,
          default_step_timeout_ms: 300_000,
          agent_poll_ms: 1_000,
          agent_step_timeout_ms: 300_000,
          approval_ttl_seconds: 86_400
        }
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
