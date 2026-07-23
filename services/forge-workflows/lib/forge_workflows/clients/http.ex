defmodule ForgeWorkflows.Clients.Http do
  @moduledoc false

  @spec request(atom(), String.t(), [{String.t(), String.t()}], binary() | nil, pos_integer()) ::
          {:ok, pos_integer(), binary()} | {:error, term()}
  def request(method, url, headers, body, timeout_ms)
      when method in [:get, :post, :put, :delete] and is_binary(url) and is_integer(timeout_ms) do
    _ = Application.ensure_all_started(:inets)
    _ = Application.ensure_all_started(:ssl)

    url_char = String.to_charlist(url)
    hdrs = Enum.map(headers, fn {k, v} -> {String.to_charlist(k), String.to_charlist(v)} end)

    http_opts = [
      timeout: timeout_ms,
      connect_timeout: min(timeout_ms, 5_000)
    ]

    opts = [body_format: :binary]

    req =
      case {method, body} do
        {:get, _} ->
          {url_char, hdrs}

        {m, nil} when m in [:post, :put, :delete] ->
          {url_char, hdrs, ~c"application/json", ""}

        {m, bin} when m in [:post, :put, :delete] and is_binary(bin) ->
          {url_char, hdrs, ~c"application/json", bin}
      end

    case :httpc.request(method, req, http_opts, opts) do
      {:ok, {{_, status, _}, _resp_headers, resp_body}} when is_binary(resp_body) ->
        {:ok, status, resp_body}

      {:ok, {{_, status, _}, _resp_headers, resp_body}} ->
        {:ok, status, IO.iodata_to_binary(resp_body)}

      {:error, reason} ->
        {:error, reason}
    end
  end
end
