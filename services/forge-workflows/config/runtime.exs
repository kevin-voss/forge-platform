import Config

# Runtime env is read in ForgeWorkflows.Config (PORT / DB URL required at Application.start).
# No distributed Erlang for this service container.

if config_env() == :prod do
  database_url = System.get_env("FORGE_WORKFLOWS_DATABASE_URL")

  if is_binary(database_url) and String.trim(database_url) != "" do
    config :forge_workflows, ForgeWorkflows.Repo,
      url: String.trim(database_url),
      pool_size: String.to_integer(System.get_env("FORGE_WORKFLOWS_POOL_SIZE") || "5")
  end
end
