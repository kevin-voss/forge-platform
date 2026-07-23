defmodule ForgeWorkflows.Saga.Log do
  @moduledoc false

  import Ecto.Query

  alias ForgeWorkflows.Repo
  alias ForgeWorkflows.Schemas.SagaEntry

  @spec record(String.t(), String.t(), String.t(), map()) ::
          {:ok, SagaEntry.t()} | {:error, term()}
  def record(run_id, step_id, compensator, args \\ %{})
      when is_binary(run_id) and is_binary(step_id) and is_binary(compensator) and is_map(args) do
    case get_by_run_step(run_id, step_id) do
      %SagaEntry{} = existing ->
        {:ok, existing}

      nil ->
        now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

        attrs = %{
          id: Ecto.UUID.generate(),
          run_id: run_id,
          step_id: step_id,
          compensator: compensator,
          args: args,
          status: "pending",
          inserted_at: now,
          updated_at: now
        }

        case %SagaEntry{}
             |> SagaEntry.changeset(attrs)
             |> Repo.insert() do
          {:ok, entry} ->
            {:ok, entry}

          {:error, changeset} ->
            case get_by_run_step(run_id, step_id) do
              %SagaEntry{} = existing -> {:ok, existing}
              nil -> {:error, changeset}
            end
        end
    end
  end

  @spec get_by_run_step(String.t(), String.t()) :: SagaEntry.t() | nil
  def get_by_run_step(run_id, step_id) when is_binary(run_id) and is_binary(step_id) do
    from(e in SagaEntry, where: e.run_id == ^run_id and e.step_id == ^step_id)
    |> Repo.one()
  end

  @spec list_for_run(String.t()) :: [SagaEntry.t()]
  def list_for_run(run_id) when is_binary(run_id) do
    from(e in SagaEntry,
      where: e.run_id == ^run_id,
      order_by: [asc: e.inserted_at, asc: e.step_id]
    )
    |> Repo.all()
  end

  @spec list_actionable_reverse(String.t()) :: [SagaEntry.t()]
  def list_actionable_reverse(run_id) when is_binary(run_id) do
    from(e in SagaEntry,
      where: e.run_id == ^run_id and e.status in ^["pending", "running", "failed"],
      order_by: [desc: e.inserted_at, desc: e.step_id]
    )
    |> Repo.all()
  end

  @spec claim(String.t()) :: {:ok, SagaEntry.t()} | {:error, :not_found | :already_done}
  def claim(entry_id) when is_binary(entry_id) do
    Repo.transaction(fn ->
      entry =
        from(e in SagaEntry, where: e.id == ^entry_id, lock: "FOR UPDATE")
        |> Repo.one()

      cond do
        is_nil(entry) ->
          Repo.rollback(:not_found)

        entry.status == "compensated" ->
          Repo.rollback(:already_done)

        true ->
          {:ok, updated} =
            entry
            |> SagaEntry.changeset(%{status: "running", error: nil})
            |> Repo.update()

          updated
      end
    end)
  end

  @spec mark_compensated(String.t(), map()) :: {:ok, SagaEntry.t()} | {:error, term()}
  def mark_compensated(entry_id, result) when is_binary(entry_id) and is_map(result) do
    case Repo.get(SagaEntry, entry_id) do
      nil ->
        {:error, :not_found}

      %SagaEntry{status: "compensated"} = entry ->
        {:ok, entry}

      entry ->
        entry
        |> SagaEntry.changeset(%{status: "compensated", result: result, error: nil})
        |> Repo.update()
    end
  end

  @spec mark_failed(String.t(), String.t()) :: {:ok, SagaEntry.t()} | {:error, term()}
  def mark_failed(entry_id, error) when is_binary(entry_id) and is_binary(error) do
    case Repo.get(SagaEntry, entry_id) do
      nil ->
        {:error, :not_found}

      entry ->
        entry
        |> SagaEntry.changeset(%{status: "failed", error: error})
        |> Repo.update()
    end
  end

  @spec has_pending?(String.t()) :: boolean()
  def has_pending?(run_id) when is_binary(run_id) do
    from(e in SagaEntry,
      where: e.run_id == ^run_id and e.status in ^["pending", "running"],
      select: count(e.id)
    )
    |> Repo.one()
    |> Kernel.>(0)
  end

  @spec to_api([SagaEntry.t()]) :: [map()]
  def to_api(entries) when is_list(entries) do
    Enum.map(entries, fn e ->
      %{
        "step_id" => e.step_id,
        "compensator" => e.compensator,
        "status" => e.status
      }
      |> maybe_put("args", e.args)
      |> maybe_put("result", e.result)
      |> maybe_put("error", e.error)
    end)
  end

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)
end
