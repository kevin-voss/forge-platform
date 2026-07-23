defmodule ForgeWorkflowsWeb.Router do
  @moduledoc false

  use Plug.Router

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Health
  alias ForgeWorkflows.Runs

  plug ForgeWorkflowsWeb.RequestId
  plug :match
  plug Plug.Parsers,
    parsers: [:json],
    pass: ["*/*"],
    json_decoder: Jason

  plug :dispatch

  get "/health/live" do
    send_json(conn, 200, Health.live())
  end

  get "/health/ready" do
    body = Health.ready()
    send_json(conn, Health.ready_status_code(), body)
  end

  get "/" do
    cfg = Application.fetch_env!(:forge_workflows, :runtime_config)
    started_at = Application.fetch_env!(:forge_workflows, :started_at)
    uptime = System.monotonic_time(:second) - started_at

    send_json(conn, 200, %{
      service: cfg.service_name,
      language: "elixir",
      status: "running",
      version: cfg.service_version,
      uptime_seconds: uptime
    })
  end

  get "/v1/workflows" do
    workflows =
      Loader.list()
      |> Enum.map(fn wf ->
        %{
          "name" => wf.name,
          "steps" =>
            Enum.map(wf.steps, fn step ->
              %{
                "id" => step.id,
                "type" => step.type
              }
              |> maybe_put("message", Map.get(step, :message))
              |> maybe_put("delay_ms", Map.get(step, :delay_ms))
            end)
        }
      end)

    send_json(conn, 200, %{"workflows" => workflows})
  end

  post "/v1/workflows/:name/runs" do
    with {:ok, project_id} <- project_id(conn),
         {:ok, input} <- read_input(conn) do
      case Runs.start_run(name, project_id, input) do
        {:ok, run} ->
          send_json(conn, 202, %{"run_id" => run.id, "status" => "running"})

        {:error, :workflow_not_found} ->
          send_json(conn, 404, %{
            "error" => "workflow not found: #{name}",
            "code" => "workflow_not_found"
          })

        {:error, reason} ->
          send_json(conn, 500, %{
            "error" => "failed to start run: #{inspect(reason)}",
            "code" => "run_start_failed"
          })
      end
    else
      {:error, :project_required} ->
        send_json(conn, 400, %{
          "error" => "X-Forge-Project header is required",
          "code" => "project_required"
        })

      {:error, :invalid_json} ->
        send_json(conn, 400, %{"error" => "invalid JSON body", "code" => "invalid_json"})
    end
  end

  get "/v1/runs/:id" do
    with {:ok, project_id} <- project_id(conn) do
      case Runs.get_run(id, project_id) do
        nil ->
          send_json(conn, 404, %{"error" => "run not found: #{id}", "code" => "run_not_found"})

        run ->
          send_json(conn, 200, Runs.to_api(run))
      end
    else
      {:error, :project_required} ->
        send_json(conn, 400, %{
          "error" => "X-Forge-Project header is required",
          "code" => "project_required"
        })
    end
  end

  get "/v1/runs" do
    with {:ok, project_id} <- project_id(conn) do
      runs = Enum.map(Runs.list_runs(project_id), &Runs.to_api/1)
      send_json(conn, 200, %{"runs" => runs})
    else
      {:error, :project_required} ->
        send_json(conn, 400, %{
          "error" => "X-Forge-Project header is required",
          "code" => "project_required"
        })
    end
  end

  match _ do
    send_json(conn, 404, %{error: "not_found"})
  end

  defp project_id(conn) do
    case get_req_header(conn, "x-forge-project") do
      [value | _] ->
        trimmed = String.trim(value)

        if trimmed == "" do
          {:error, :project_required}
        else
          {:ok, trimmed}
        end

      _ ->
        {:error, :project_required}
    end
  end

  defp read_input(conn) do
    case conn.body_params do
      %Plug.Conn.Unfetched{} ->
        {:ok, %{}}

      params when is_map(params) ->
        input = Map.get(params, "input", %{})

        if is_map(input) do
          {:ok, input}
        else
          {:error, :invalid_json}
        end

      _ ->
        {:ok, %{}}
    end
  end

  defp maybe_put(map, _key, nil), do: map
  defp maybe_put(map, key, value), do: Map.put(map, key, value)

  defp send_json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body) <> "\n")
  end
end
