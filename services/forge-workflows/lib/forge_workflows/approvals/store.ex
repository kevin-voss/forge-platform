defmodule ForgeWorkflows.Approvals.Store do
  @moduledoc false

  import Ecto.Query

  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Repo
  alias ForgeWorkflows.Schemas.Approval

  @pending "pending"
  @terminal ~w(approved denied expired)

  @spec create(String.t(), String.t(), String.t(), String.t() | nil, pos_integer()) ::
          {:ok, Approval.t()} | {:error, term()}
  def create(run_id, step_id, project_id, prompt, ttl_seconds)
      when is_binary(run_id) and is_binary(step_id) and is_binary(project_id) and
             is_integer(ttl_seconds) and ttl_seconds >= 1 do
    case get_by_run_step(run_id, step_id) do
      %Approval{} = existing ->
        {:ok, existing}

      nil ->
        now = DateTime.utc_now() |> DateTime.truncate(:microsecond)
        expires_at = DateTime.add(now, ttl_seconds, :second)

        attrs = %{
          id: Ecto.UUID.generate(),
          run_id: run_id,
          step_id: step_id,
          project_id: project_id,
          prompt: prompt,
          status: @pending,
          expires_at: expires_at,
          inserted_at: now,
          updated_at: now
        }

        case %Approval{} |> Approval.changeset(attrs) |> Repo.insert() do
          {:ok, approval} = ok ->
            Metrics.inc_approval(@pending)
            log("approval created", approval, %{})
            ok

          other ->
            other
        end
    end
  end

  @spec get(String.t(), String.t()) :: Approval.t() | nil
  def get(approval_id, project_id)
      when is_binary(approval_id) and is_binary(project_id) do
    from(a in Approval, where: a.id == ^approval_id and a.project_id == ^project_id)
    |> Repo.one()
  end

  @spec get_record(String.t()) :: Approval.t() | nil
  def get_record(approval_id) when is_binary(approval_id) do
    Repo.get(Approval, approval_id)
  end

  @spec get_by_run_step(String.t(), String.t()) :: Approval.t() | nil
  def get_by_run_step(run_id, step_id) when is_binary(run_id) and is_binary(step_id) do
    from(a in Approval, where: a.run_id == ^run_id and a.step_id == ^step_id)
    |> Repo.one()
  end

  @spec list_for_run(String.t(), String.t()) :: [Approval.t()]
  def list_for_run(run_id, project_id) when is_binary(run_id) and is_binary(project_id) do
    from(a in Approval,
      where: a.run_id == ^run_id and a.project_id == ^project_id,
      order_by: [asc: a.inserted_at]
    )
    |> Repo.all()
  end

  @spec list_for_project(String.t()) :: [Approval.t()]
  def list_for_project(project_id) when is_binary(project_id) do
    from(a in Approval,
      where: a.project_id == ^project_id,
      order_by: [desc: a.inserted_at]
    )
    |> Repo.all()
  end

  @spec list_expired_pending(DateTime.t()) :: [Approval.t()]
  def list_expired_pending(now \\ DateTime.utc_now()) do
    now = DateTime.truncate(now, :microsecond)

    from(a in Approval,
      where: a.status == ^@pending and not is_nil(a.expires_at) and a.expires_at <= ^now,
      order_by: [asc: a.expires_at]
    )
    |> Repo.all()
  end

  @spec decide(String.t(), String.t(), String.t(), String.t(), String.t() | nil) ::
          {:ok, Approval.t()}
          | {:error, :not_found}
          | {:error, :terminal}
          | {:error, term()}
  def decide(approval_id, project_id, status, decided_by, reason)
      when status in ["approved", "denied"] and is_binary(approval_id) and is_binary(project_id) and
             is_binary(decided_by) do
    Repo.transaction(fn ->
      approval =
        from(a in Approval,
          where: a.id == ^approval_id and a.project_id == ^project_id,
          lock: "FOR UPDATE"
        )
        |> Repo.one()

      cond do
        is_nil(approval) ->
          Repo.rollback(:not_found)

        approval.status in @terminal ->
          Repo.rollback(:terminal)

        true ->
          now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

          {:ok, updated} =
            approval
            |> Approval.changeset(%{
              status: status,
              decided_by: decided_by,
              reason: reason,
              decided_at: now,
              updated_at: now
            })
            |> Repo.update()

          Metrics.inc_approval(status)
          observe_decision(updated)
          log("approval #{status}", updated, %{actor: decided_by, reason: reason})
          updated
      end
    end)
    |> case do
      {:ok, approval} -> {:ok, approval}
      {:error, :not_found} -> {:error, :not_found}
      {:error, :terminal} -> {:error, :terminal}
      {:error, reason} -> {:error, reason}
    end
  end

  @spec expire(Approval.t()) :: {:ok, Approval.t()} | {:error, term()}
  def expire(%Approval{} = approval) do
    Repo.transaction(fn ->
      locked =
        from(a in Approval, where: a.id == ^approval.id, lock: "FOR UPDATE")
        |> Repo.one()

      cond do
        is_nil(locked) ->
          Repo.rollback(:not_found)

        locked.status in @terminal ->
          locked

        true ->
          now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

          {:ok, updated} =
            locked
            |> Approval.changeset(%{
              status: "expired",
              decided_by: "system",
              reason: "approval expired",
              decided_at: now,
              updated_at: now
            })
            |> Repo.update()

          Metrics.inc_approval("expired")
          observe_decision(updated)
          log("approval expired", updated, %{actor: "system"})
          updated
      end
    end)
  end

  @spec to_api(Approval.t()) :: map()
  def to_api(%Approval{} = a) do
    %{
      "id" => a.id,
      "run_id" => a.run_id,
      "step_id" => a.step_id,
      "project_id" => a.project_id,
      "prompt" => a.prompt,
      "status" => a.status
    }
    |> maybe_put("decided_by", a.decided_by)
    |> maybe_put("reason", a.reason)
    |> maybe_put("expires_at", a.expires_at && DateTime.to_iso8601(a.expires_at))
    |> maybe_put("decided_at", a.decided_at && DateTime.to_iso8601(a.decided_at))
    |> maybe_put("created_at", a.inserted_at && DateTime.to_iso8601(a.inserted_at))
  end

  defp observe_decision(%Approval{inserted_at: inserted, decided_at: decided})
       when not is_nil(inserted) and not is_nil(decided) do
    ms = DateTime.diff(decided, inserted, :millisecond)
    Metrics.observe_approval_decision_ms(max(ms, 0))
  end

  defp observe_decision(_), do: :ok

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)

  defp log(message, %Approval{} = approval, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(
      service,
      message,
      Map.merge(
        %{
          approval_id: approval.id,
          run_id: approval.run_id,
          step_id: approval.step_id,
          status: approval.status
        },
        fields
      )
    )
  end
end
