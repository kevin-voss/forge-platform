defmodule ForgeWorkflows.Repo.Migrations.SagaLog do
  use Ecto.Migration

  def change do
    create table(:workflow_saga_log, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :run_id, references(:workflow_runs, type: :uuid, on_delete: :delete_all), null: false
      add :step_id, :text, null: false
      add :compensator, :text, null: false
      add :args, :map, null: false, default: %{}
      add :status, :text, null: false
      add :result, :map
      add :error, :text
      add :inserted_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:workflow_saga_log, [:run_id, :step_id])
    create index(:workflow_saga_log, [:run_id, :status])
    create index(:workflow_saga_log, [:run_id, :inserted_at])
  end
end
