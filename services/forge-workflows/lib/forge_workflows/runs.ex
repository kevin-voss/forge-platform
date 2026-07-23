defmodule ForgeWorkflows.Runs do
  @moduledoc false

  import Ecto.Query

  alias Ecto.Multi
  alias ForgeWorkflows.Approvals.Store, as: ApprovalStore
  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Engine.RunSupervisor
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Repo
  alias ForgeWorkflows.Schemas.Run
  alias ForgeWorkflows.Schemas.Step

  @terminal ~w(completed failed)

  @spec start_run(String.t(), String.t(), map()) ::
          {:ok, Run.t()} | {:error, :workflow_not_found} | {:error, term()}
  def start_run(workflow_name, project_id, input)
      when is_binary(workflow_name) and is_binary(project_id) and is_map(input) do
    case Loader.get(workflow_name) do
      nil ->
        {:error, :workflow_not_found}

      workflow ->
        run_id = Ecto.UUID.generate()
        now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

        # Top-level definition steps (including conditional targets and parallel
        # string-ref branches). Inline parallel branches are created at fan-out.
        step_rows =
          Enum.map(workflow.steps, fn step ->
            %{
              id: Ecto.UUID.generate(),
              run_id: run_id,
              step_id: step.id,
              type: step.type,
              status: "pending",
              attempt: 0,
              wake_at: nil,
              parent_step_id: parent_for_ref_branch(workflow.steps, step.id),
              inserted_at: now,
              updated_at: now
            }
          end)

        run_attrs = %{
          id: run_id,
          workflow: workflow.name,
          project_id: project_id,
          status: "queued",
          input: input,
          current_step: nil,
          inserted_at: now,
          updated_at: now
        }

        multi =
          Multi.new()
          |> Multi.insert(:run, Run.changeset(%Run{}, run_attrs))
          |> Multi.insert_all(:steps, Step, step_rows)

        case Repo.transaction(multi) do
          {:ok, %{run: run}} ->
            Metrics.inc_run("queued")
            _ = RunSupervisor.start_run(run.id)
            {:ok, run}

          {:error, _step, reason, _} ->
            {:error, reason}
        end
    end
  end

  @spec get_run(String.t(), String.t()) :: Run.t() | nil
  def get_run(run_id, project_id) when is_binary(run_id) and is_binary(project_id) do
    from(r in Run,
      where: r.id == ^run_id and r.project_id == ^project_id,
      preload: [steps: ^from(s in Step, order_by: [asc: s.inserted_at])]
    )
    |> Repo.one()
  end

  @spec list_runs(String.t()) :: [Run.t()]
  def list_runs(project_id) when is_binary(project_id) do
    from(r in Run,
      where: r.project_id == ^project_id,
      order_by: [desc: r.inserted_at],
      preload: [steps: ^from(s in Step, order_by: [asc: s.inserted_at])]
    )
    |> Repo.all()
  end

  @spec get_run_record(String.t()) :: Run.t() | nil
  def get_run_record(run_id) when is_binary(run_id) do
    Repo.get(Run, run_id)
  end

  @spec list_inflight_run_ids() :: [String.t()]
  def list_inflight_run_ids do
    from(r in Run, where: r.status not in ^@terminal, select: r.id, order_by: [asc: r.inserted_at])
    |> Repo.all()
  end

  @spec get_step(String.t(), String.t()) :: Step.t() | nil
  def get_step(run_id, step_id) when is_binary(run_id) and is_binary(step_id) do
    from(s in Step, where: s.run_id == ^run_id and s.step_id == ^step_id)
    |> Repo.one()
  end

  @spec list_due_waiting_steps(DateTime.t()) :: [Step.t()]
  def list_due_waiting_steps(now \\ DateTime.utc_now()) do
    now = DateTime.truncate(now, :microsecond)

    from(s in Step,
      where: s.status == "waiting" and not is_nil(s.wake_at) and s.wake_at <= ^now,
      order_by: [asc: s.wake_at]
    )
    |> Repo.all()
  end

  @spec ensure_child_step(String.t(), String.t(), String.t(), String.t()) ::
          {:ok, Step.t()} | {:error, term()}
  def ensure_child_step(run_id, parent_step_id, step_id, type)
      when is_binary(run_id) and is_binary(parent_step_id) and is_binary(step_id) and
             is_binary(type) do
    case get_step(run_id, step_id) do
      %Step{} = step ->
        {:ok, step}

      nil ->
        now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

        %Step{}
        |> Step.changeset(%{
          id: Ecto.UUID.generate(),
          run_id: run_id,
          step_id: step_id,
          type: type,
          status: "pending",
          attempt: 0,
          parent_step_id: parent_step_id,
          inserted_at: now,
          updated_at: now
        })
        |> Repo.insert()
    end
  end

  @spec mark_run_running(String.t(), String.t() | nil) :: {:ok, Run.t()} | {:error, term()}
  def mark_run_running(run_id, current_step) do
    case Repo.get(Run, run_id) do
      nil ->
        {:error, :not_found}

      %Run{status: status} = run when status in @terminal ->
        {:ok, run}

      run ->
        case run
             |> Run.changeset(%{status: "running", current_step: current_step})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_run("running")
            ok

          other ->
            other
        end
    end
  end

  @spec mark_run_awaiting_approval(String.t(), String.t() | nil) ::
          {:ok, Run.t()} | {:error, term()}
  def mark_run_awaiting_approval(run_id, current_step) do
    case Repo.get(Run, run_id) do
      nil ->
        {:error, :not_found}

      %Run{status: status} = run when status in @terminal ->
        {:ok, run}

      run ->
        case run
             |> Run.changeset(%{status: "awaiting_approval", current_step: current_step})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_run("awaiting_approval")
            ok

          other ->
            other
        end
    end
  end

  @spec mark_run_completed(String.t(), map()) :: {:ok, Run.t()} | {:error, term()}
  def mark_run_completed(run_id, result) when is_map(result) do
    case Repo.get(Run, run_id) do
      nil ->
        {:error, :not_found}

      run ->
        case run
             |> Run.changeset(%{status: "completed", result: result, current_step: nil, error: nil})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_run("completed")
            ok

          other ->
            other
        end
    end
  end

  @spec mark_run_failed(String.t(), String.t()) :: {:ok, Run.t()} | {:error, term()}
  def mark_run_failed(run_id, error) when is_binary(error) do
    case Repo.get(Run, run_id) do
      nil ->
        {:error, :not_found}

      run ->
        case run
             |> Run.changeset(%{status: "failed", error: error})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_run("failed")
            ok

          other ->
            other
        end
    end
  end

  @spec begin_step(String.t(), String.t()) ::
          {:ok, :execute, Step.t()}
          | {:ok, :skip, Step.t()}
          | {:ok, :wake, Step.t()}
          | {:error, term()}
  def begin_step(run_id, step_id) do
    Repo.transaction(fn ->
      step =
        from(s in Step,
          where: s.run_id == ^run_id and s.step_id == ^step_id,
          lock: "FOR UPDATE"
        )
        |> Repo.one()

      cond do
        is_nil(step) ->
          Repo.rollback(:not_found)

        step.status in ["completed", "skipped"] ->
          {:skip, step}

        step.status == "waiting" ->
          {:wake, step}

        true ->
          {:ok, updated} =
            step
            |> Step.changeset(%{
              status: "running",
              attempt: step.attempt + 1,
              error: nil,
              wake_at: nil
            })
            |> Repo.update()

          Metrics.inc_step("running")
          {:execute, updated}
      end
    end)
    |> case do
      {:ok, {:skip, step}} -> {:ok, :skip, step}
      {:ok, {:wake, step}} -> {:ok, :wake, step}
      {:ok, {:execute, step}} -> {:ok, :execute, step}
      {:error, reason} -> {:error, reason}
    end
  end

  @spec rebegin_after_wait(String.t(), String.t()) :: {:ok, Step.t()} | {:error, term()}
  def rebegin_after_wait(run_id, step_id) do
    Repo.transaction(fn ->
      step =
        from(s in Step,
          where: s.run_id == ^run_id and s.step_id == ^step_id,
          lock: "FOR UPDATE"
        )
        |> Repo.one()

      cond do
        is_nil(step) ->
          Repo.rollback(:not_found)

        step.status == "completed" ->
          step

        true ->
          {:ok, updated} =
            step
            |> Step.changeset(%{
              status: "running",
              attempt: step.attempt + 1,
              error: nil,
              wake_at: nil
            })
            |> Repo.update()

          Metrics.inc_step("running")
          updated
      end
    end)
  end

  @spec mark_step_waiting(String.t(), String.t(), DateTime.t()) ::
          {:ok, Step.t()} | {:error, term()}
  def mark_step_waiting(run_id, step_id, wake_at) do
    case get_step(run_id, step_id) do
      nil ->
        {:error, :not_found}

      step ->
        step
        |> Step.changeset(%{status: "waiting", wake_at: wake_at, error: nil})
        |> Repo.update()
        |> case do
          {:ok, _} = ok ->
            Metrics.inc_step("waiting")
            ok

          other ->
            other
        end
    end
  end

  @spec skip_step(String.t(), String.t(), map() | nil) :: {:ok, Step.t()} | {:error, term()}
  def skip_step(run_id, step_id, output \\ %{"skipped" => true}) do
    case get_step(run_id, step_id) do
      nil ->
        {:error, :not_found}

      %Step{status: status} = step when status in ["completed", "skipped"] ->
        {:ok, step}

      step ->
        case step
             |> Step.changeset(%{status: "skipped", output: output, error: nil, wake_at: nil})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_step("skipped")
            ok

          other ->
            other
        end
    end
  end

  @spec complete_step(String.t(), String.t(), map()) :: {:ok, Step.t()} | {:error, term()}
  def complete_step(run_id, step_id, output) when is_map(output) do
    case get_step(run_id, step_id) do
      nil ->
        {:error, :not_found}

      %Step{status: "completed"} = step ->
        {:ok, step}

      step ->
        case step
             |> Step.changeset(%{status: "completed", output: output, error: nil, wake_at: nil})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_step("completed")
            ok

          other ->
            other
        end
    end
  end

  @spec fail_step(String.t(), String.t(), String.t()) :: {:ok, Step.t()} | {:error, term()}
  def fail_step(run_id, step_id, error) when is_binary(error) do
    case get_step(run_id, step_id) do
      nil ->
        {:error, :not_found}

      step ->
        case step
             |> Step.changeset(%{status: "failed", error: error, wake_at: nil})
             |> Repo.update() do
          {:ok, _} = ok ->
            Metrics.inc_step("failed")
            ok

          other ->
            other
        end
    end
  end

  @spec to_api(Run.t()) :: map()
  def to_api(%Run{} = run) do
    steps =
      (run.steps || [])
      |> Enum.map(fn step ->
        %{
          "id" => step.step_id,
          "type" => step.type,
          "status" => step.status,
          "attempt" => step.attempt
        }
        |> maybe_put("output", step.output)
        |> maybe_put("error", step.error)
        |> maybe_put("wake_at", step.wake_at && DateTime.to_iso8601(step.wake_at))
        |> maybe_put("parent_step_id", step.parent_step_id)
      end)

    pending =
      if run.status == "awaiting_approval" and is_binary(run.current_step) do
        case ApprovalStore.get_by_run_step(run.id, run.current_step) do
          nil -> nil
          approval -> ApprovalStore.to_api(approval)
        end
      else
        nil
      end

    %{
      "run_id" => run.id,
      "workflow" => run.workflow,
      "project_id" => run.project_id,
      "status" => run.status,
      "input" => run.input || %{},
      "steps" => steps
    }
    |> maybe_put("result", run.result)
    |> maybe_put("error", run.error)
    |> maybe_put("current_step", run.current_step)
    |> maybe_put("pending_approval", pending)
  end

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)

  defp parent_for_ref_branch(steps, step_id) do
    Enum.find_value(steps, fn
      %{type: "parallel", id: parent_id, branches: branches} ->
        if Enum.any?(branches || [], &(&1.type == "ref" and &1.id == step_id)), do: parent_id

      _ ->
        nil
    end)
  end
end
