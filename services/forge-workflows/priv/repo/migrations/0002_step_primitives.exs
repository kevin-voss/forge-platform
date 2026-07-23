defmodule ForgeWorkflows.Repo.Migrations.StepPrimitives do
  use Ecto.Migration

  def change do
    alter table(:workflow_steps) do
      add :wake_at, :utc_datetime_usec
      add :parent_step_id, :text
    end

    create index(:workflow_steps, [:wake_at])
    create index(:workflow_steps, [:parent_step_id])
    create index(:workflow_steps, [:run_id, :status])
  end
end
