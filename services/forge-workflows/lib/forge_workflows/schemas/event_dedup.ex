defmodule ForgeWorkflows.Schemas.EventDedup do
  @moduledoc false

  use Ecto.Schema
  import Ecto.Changeset

  @primary_key {:id, :binary_id, autogenerate: false}
  @timestamps_opts [type: :utc_datetime_usec]

  schema "event_dedup" do
    field :event_id, :string
    field :workflow, :string
    field :run_id, :binary_id
    field :project_id, :string
    field :event_type, :string

    timestamps(inserted_at: :inserted_at, updated_at: :updated_at)
  end

  def changeset(row, attrs) do
    row
    |> cast(attrs, [:id, :event_id, :workflow, :run_id, :project_id, :event_type])
    |> validate_required([:id, :event_id, :workflow, :project_id, :event_type])
    |> unique_constraint([:event_id, :workflow], name: :event_dedup_event_id_workflow_index)
  end
end
