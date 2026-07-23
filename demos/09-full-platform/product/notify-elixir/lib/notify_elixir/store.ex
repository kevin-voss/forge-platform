defmodule NotifyElixir.Store do
  @moduledoc false

  use Agent

  def start_link(_opts) do
    Agent.start_link(fn -> [] end, name: __MODULE__)
  end

  def put(notification) do
    Agent.update(__MODULE__, fn list -> [notification | list] end)
    notification
  end

  def list do
    Agent.get(__MODULE__, fn list -> Enum.reverse(list) end)
  end
end
