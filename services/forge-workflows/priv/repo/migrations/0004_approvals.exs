defmodule ForgeWorkflows.Repo.Migrations.Approvals do
  use Ecto.Migration

  def change do
    create table(:workflow_approvals, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :run_id, references(:workflow_runs, type: :uuid, on_delete: :delete_all), null: false
      add :step_id, :text, null: false
      add :project_id, :text, null: false
      add :prompt, :text
      add :status, :text, null: false
      add :decided_by, :text
      add :reason, :text
      add :expires_at, :utc_datetime_usec
      add :decided_at, :utc_datetime_usec
      add :inserted_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:workflow_approvals, [:run_id, :step_id])
    create index(:workflow_approvals, [:project_id])
    create index(:workflow_approvals, [:status])
    create index(:workflow_approvals, [:expires_at])
  end
end
