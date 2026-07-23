defmodule ForgeWorkflows.Schemas.Approval do
  @moduledoc false

  use Ecto.Schema
  import Ecto.Changeset

  @primary_key {:id, :binary_id, autogenerate: false}
  @foreign_key_type :binary_id
  @timestamps_opts [type: :utc_datetime_usec]

  schema "workflow_approvals" do
    field :run_id, :binary_id
    field :step_id, :string
    field :project_id, :string
    field :prompt, :string
    field :status, :string
    field :decided_by, :string
    field :reason, :string
    field :expires_at, :utc_datetime_usec
    field :decided_at, :utc_datetime_usec

    timestamps(inserted_at: :inserted_at, updated_at: :updated_at)
  end

  @statuses ~w(pending approved denied expired)

  def changeset(approval, attrs) do
    approval
    |> cast(attrs, [
      :id,
      :run_id,
      :step_id,
      :project_id,
      :prompt,
      :status,
      :decided_by,
      :reason,
      :expires_at,
      :decided_at
    ])
    |> validate_required([:id, :run_id, :step_id, :project_id, :status])
    |> validate_inclusion(:status, @statuses)
    |> unique_constraint([:run_id, :step_id], name: :workflow_approvals_run_id_step_id_index)
  end
end
