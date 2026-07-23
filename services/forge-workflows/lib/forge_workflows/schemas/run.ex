defmodule ForgeWorkflows.Schemas.Run do
  @moduledoc false

  use Ecto.Schema
  import Ecto.Changeset

  @primary_key {:id, :binary_id, autogenerate: false}
  @foreign_key_type :binary_id
  @timestamps_opts [type: :utc_datetime_usec]

  schema "workflow_runs" do
    field :workflow, :string
    field :project_id, :string
    field :status, :string
    field :input, :map, default: %{}
    field :result, :map
    field :error, :string
    field :current_step, :string

    has_many :steps, ForgeWorkflows.Schemas.Step, foreign_key: :run_id, references: :id

    timestamps(inserted_at: :inserted_at, updated_at: :updated_at)
  end

  @statuses ~w(queued running awaiting_approval compensating completed failed)

  def changeset(run, attrs) do
    run
    |> cast(attrs, [
      :id,
      :workflow,
      :project_id,
      :status,
      :input,
      :result,
      :error,
      :current_step
    ])
    |> validate_required([:id, :workflow, :project_id, :status])
    |> validate_inclusion(:status, @statuses)
  end
end
