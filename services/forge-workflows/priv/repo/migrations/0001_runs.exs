defmodule ForgeWorkflows.Repo.Migrations.CreateRuns do
  use Ecto.Migration

  def change do
    create table(:workflow_runs, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :workflow, :text, null: false
      add :project_id, :text, null: false
      add :status, :text, null: false
      add :input, :map
      add :result, :map
      add :error, :text
      add :current_step, :text
      add :inserted_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create index(:workflow_runs, [:project_id])
    create index(:workflow_runs, [:status])
    create index(:workflow_runs, [:workflow])

    create table(:workflow_steps, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :run_id, references(:workflow_runs, type: :uuid, on_delete: :delete_all), null: false
      add :step_id, :text, null: false
      add :type, :text, null: false
      add :status, :text, null: false
      add :output, :map
      add :error, :text
      add :attempt, :integer, null: false, default: 0
      add :inserted_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:workflow_steps, [:run_id, :step_id])
    create index(:workflow_steps, [:status])
  end
end
