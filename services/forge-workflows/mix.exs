defmodule ForgeWorkflows.MixProject do
  use Mix.Project

  def project do
    [
      app: :forge_workflows,
      version: "0.1.0",
      elixir: "~> 1.17",
      start_permanent: Mix.env() == :prod,
      deps: deps(),
      releases: [
        forge_workflows: [
          include_executables_for: [:unix],
          applications: [runtime_tools: :permanent]
        ]
      ]
    ]
  end

  def application do
    [
      extra_applications: [:logger],
      mod: {ForgeWorkflows.Application, []}
    ]
  end

  defp deps do
    [
      {:bandit, "~> 1.6"},
      {:plug, "~> 1.16"},
      {:jason, "~> 1.4"},
      {:ecto_sql, "~> 3.12"},
      {:postgrex, ">= 0.0.0"},
      {:yaml_elixir, "~> 2.9"}
    ]
  end

end
