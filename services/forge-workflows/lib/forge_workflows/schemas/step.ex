defmodule ForgeWorkflows.Schemas.Step do
  @moduledoc false

  use Ecto.Schema
  import Ecto.Changeset

  @primary_key {:id, :binary_id, autogenerate: false}
  @foreign_key_type :binary_id
  @timestamps_opts [type: :utc_datetime_usec]

  schema "workflow_steps" do
    field :run_id, :binary_id
    field :step_id, :string
    field :type, :string
    field :status, :string
    field :output, :map
    field :error, :string
    field :attempt, :integer, default: 0

    belongs_to :run, ForgeWorkflows.Schemas.Run,
      define_field: false,
      foreign_key: :run_id,
      type: :binary_id

    timestamps(inserted_at: :inserted_at, updated_at: :updated_at)
  end

  @statuses ~w(pending running completed failed skipped)

  def changeset(step, attrs) do
    step
    |> cast(attrs, [:id, :run_id, :step_id, :type, :status, :output, :error, :attempt])
    |> validate_required([:id, :run_id, :step_id, :type, :status])
    |> validate_inclusion(:status, @statuses)
    |> unique_constraint([:run_id, :step_id], name: :workflow_steps_run_id_step_id_index)
  end
end
