defmodule ForgeWorkflowsWeb.Router do
  @moduledoc false

  use Plug.Router

  alias ForgeWorkflows.Definitions.Loader
  alias ForgeWorkflows.Health
  alias ForgeWorkflows.Runs
  alias ForgeWorkflows.Triggers

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
              |> maybe_put("action", Map.get(step, :action))
              |> maybe_put("timeout_ms", Map.get(step, :timeout_ms))
              |> maybe_put("when", Map.get(step, :when))
              |> maybe_put("then", Map.get(step, :then))
              |> maybe_put("else", Map.get(step, :else))
              |> maybe_put("agent", Map.get(step, :agent))
              |> maybe_put("input", Map.get(step, :input))
              |> maybe_put(
                "branches",
                case Map.get(step, :branches) do
                  list when is_list(list) ->
                    Enum.map(list, fn b ->
                      %{"id" => b.id, "type" => b.type}
                    end)

                  _ ->
                    nil
                end
              )
              |> maybe_put(
                "retry",
                case Map.get(step, :retry) do
                  %{max_attempts: max, backoff: backoff, base_ms: base} ->
                    %{"max_attempts" => max, "backoff" => backoff, "base_ms" => base}

                  _ ->
                    nil
                end
              )
            end)
        }
        |> maybe_put(
          "trigger",
          case wf.trigger do
            %{event: event} -> %{"event" => event}
            _ -> nil
          end
        )
      end)

    send_json(conn, 200, %{"workflows" => workflows})
  end

  post "/v1/triggers/test" do
    with {:ok, project_id} <- project_id_or_default(conn),
         {:ok, event_type, event_id, data} <- read_trigger_test(conn) do
      case Triggers.handle_event(event_type, event_id, data, project_id) do
        {:ok, :unmatched} ->
          send_json(conn, 202, %{
            "status" => "unmatched",
            "event" => event_type,
            "event_id" => event_id,
            "runs" => []
          })

        {:ok, :duplicate, _} ->
          send_json(conn, 202, %{
            "status" => "duplicate",
            "event" => event_type,
            "event_id" => event_id,
            "runs" => []
          })

        {:ok, :started, runs} ->
          send_json(conn, 202, %{
            "status" => "started",
            "event" => event_type,
            "event_id" => event_id,
            "runs" => runs
          })

        {:error, :invalid_event_payload} ->
          send_json(conn, 400, %{
            "error" => "invalid event payload",
            "code" => "invalid_event_payload"
          })

        {:error, :invalid_event_type} ->
          send_json(conn, 400, %{
            "error" => "invalid event type",
            "code" => "invalid_event_type"
          })

        {:error, reason} ->
          send_json(conn, 500, %{
            "error" => "trigger failed: #{inspect(reason)}",
            "code" => "trigger_failed"
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

      {:error, :event_required} ->
        send_json(conn, 400, %{"error" => "event is required", "code" => "event_required"})
    end
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

  defp project_id_or_default(conn) do
    case project_id(conn) do
      {:ok, _} = ok ->
        ok

      {:error, :project_required} ->
        case Application.get_env(:forge_workflows, :runtime_config) do
          %{default_project_id: id} when is_binary(id) and id != "" -> {:ok, id}
          _ -> {:error, :project_required}
        end
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

  defp read_trigger_test(conn) do
    case conn.body_params do
      %Plug.Conn.Unfetched{} ->
        {:error, :invalid_json}

      params when is_map(params) ->
        event = Map.get(params, "event") || Map.get(params, "event_type")
        data = Map.get(params, "data", %{})
        event_id = Map.get(params, "event_id") || Map.get(params, "id")

        cond do
          not is_binary(event) or String.trim(event) == "" ->
            {:error, :event_required}

          not is_map(data) ->
            {:error, :invalid_json}

          true ->
            id =
              if is_binary(event_id) and String.trim(event_id) != "" do
                String.trim(event_id)
              else
                "test-" <> Ecto.UUID.generate()
              end

            {:ok, String.trim(event), id, data}
        end

      _ ->
        {:error, :invalid_json}
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
