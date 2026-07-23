defmodule ForgeWorkflows.Repo.Migrations.EventDedup do
  use Ecto.Migration

  def change do
    create table(:event_dedup, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :event_id, :text, null: false
      add :workflow, :text, null: false
      add :run_id, :uuid
      add :project_id, :text, null: false
      add :event_type, :text, null: false
      add :inserted_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:event_dedup, [:event_id, :workflow])
    create index(:event_dedup, [:event_type])
    create index(:event_dedup, [:run_id])
  end
end
