defmodule ForgeWorkflows.Triggers do
  @moduledoc false

  import Ecto.Query

  alias ForgeWorkflows.JsonLog
  alias ForgeWorkflows.Metrics
  alias ForgeWorkflows.Repo
  alias ForgeWorkflows.Runs
  alias ForgeWorkflows.Schemas.EventDedup
  alias ForgeWorkflows.Triggers.Registry

  @type handle_result ::
          {:ok, :unmatched}
          | {:ok, :duplicate, String.t()}
          | {:ok, :started, [map()]}
          | {:error, term()}

  @spec handle_event(String.t(), String.t(), map(), String.t()) :: handle_result()
  def handle_event(event_type, event_id, data, project_id)
      when is_binary(event_type) and is_binary(event_id) and is_map(data) and
             is_binary(project_id) do
    with :ok <- validate_event(event_type, data) do
      workflows = Registry.workflows_for(event_type)

      if workflows == [] do
        log("event unmatched", %{event_id: event_id, event: event_type})
        {:ok, :unmatched}
      else
        results =
          Enum.map(workflows, fn workflow ->
            start_idempotent(workflow, event_type, event_id, data, project_id)
          end)

        cond do
          Enum.any?(results, &match?({:error, _}, &1)) ->
            {:error, Enum.find(results, &match?({:error, _}, &1)) |> elem(1)}

          Enum.all?(results, &match?({:ok, :duplicate, _}, &1)) ->
            {:ok, :duplicate, event_id}

          true ->
            started =
              results
              |> Enum.filter(&match?({:ok, :started, _}, &1))
              |> Enum.map(fn {:ok, :started, info} -> info end)

            {:ok, :started, started}
        end
      end
    end
  end

  @spec already_seen?(String.t(), String.t()) :: boolean()
  def already_seen?(event_id, workflow) when is_binary(event_id) and is_binary(workflow) do
    from(d in EventDedup, where: d.event_id == ^event_id and d.workflow == ^workflow)
    |> Repo.exists?()
  end

  defp start_idempotent(workflow, event_type, event_id, data, project_id) do
    now = DateTime.utc_now() |> DateTime.truncate(:microsecond)

    claim = %{
      id: Ecto.UUID.generate(),
      event_id: event_id,
      workflow: workflow.name,
      project_id: project_id,
      event_type: event_type,
      inserted_at: now,
      updated_at: now
    }

    case %EventDedup{} |> EventDedup.changeset(claim) |> Repo.insert() do
      {:ok, dedup} ->
        input = %{
          "event" => data,
          "event_id" => event_id,
          "event_type" => event_type
        }

        case Runs.start_run(workflow.name, project_id, input) do
          {:ok, run} ->
            _ =
              dedup
              |> EventDedup.changeset(%{run_id: run.id})
              |> Repo.update()

            Metrics.inc_trigger(event_type)

            log("run started from event", %{
              event_id: event_id,
              event: event_type,
              run_id: run.id,
              workflow: workflow.name
            })

            {:ok, :started,
             %{
               "run_id" => run.id,
               "workflow" => workflow.name,
               "event_id" => event_id,
               "status" => run.status
             }}

          {:error, reason} ->
            _ = Repo.delete(dedup)
            {:error, reason}
        end

      {:error, %Ecto.Changeset{errors: errors}} ->
        if unique_violation?(errors) do
          log("event deduped", %{
            event_id: event_id,
            event: event_type,
            workflow: workflow.name
          })

          {:ok, :duplicate, event_id}
        else
          {:error, errors}
        end
    end
  end

  defp validate_event(event_type, data) when is_map(data) do
    cond do
      event_type == "" ->
        {:error, :invalid_event_type}

      event_type == "deployment.failed" and blank?(Map.get(data, "deployment_id")) ->
        {:error, :invalid_event_payload}

      true ->
        :ok
    end
  end

  defp validate_event(_, _), do: {:error, :invalid_event_payload}

  defp blank?(nil), do: true
  defp blank?(""), do: true
  defp blank?(value) when is_binary(value), do: String.trim(value) == ""
  defp blank?(_), do: false

  defp unique_violation?(errors) do
    Enum.any?(errors, fn
      {:event_id, {_, opts}} -> opts[:constraint] == :unique
      {_, {_, opts}} when is_list(opts) -> opts[:constraint] == :unique
      _ -> false
    end)
  end

  defp log(message, fields) do
    service =
      case Application.get_env(:forge_workflows, :runtime_config) do
        %{service_name: name} -> name
        _ -> "forge-workflows"
      end

    JsonLog.info(service, message, fields)
  end
end
