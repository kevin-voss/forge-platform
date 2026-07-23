defmodule ForgeWorkflows.Schemas.SagaEntry do
  @moduledoc false

  use Ecto.Schema
  import Ecto.Changeset

  @primary_key {:id, :binary_id, autogenerate: false}
  @foreign_key_type :binary_id
  @timestamps_opts [type: :utc_datetime_usec]

  schema "workflow_saga_log" do
    field :run_id, :binary_id
    field :step_id, :string
    field :compensator, :string
    field :args, :map, default: %{}
    field :status, :string
    field :result, :map
    field :error, :string

    timestamps(inserted_at: :inserted_at, updated_at: :updated_at)
  end

  @statuses ~w(pending running compensated failed)

  def changeset(entry, attrs) do
    entry
    |> cast(attrs, [
      :id,
      :run_id,
      :step_id,
      :compensator,
      :args,
      :status,
      :result,
      :error
    ])
    |> validate_required([:id, :run_id, :step_id, :compensator, :status])
    |> validate_inclusion(:status, @statuses)
    |> unique_constraint([:run_id, :step_id], name: :workflow_saga_log_run_id_step_id_index)
  end
end
