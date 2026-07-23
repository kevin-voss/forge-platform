defmodule ForgeWorkflows.Repo do
  @moduledoc false
  use Ecto.Repo,
    otp_app: :forge_workflows,
    adapter: Ecto.Adapters.Postgres
end
