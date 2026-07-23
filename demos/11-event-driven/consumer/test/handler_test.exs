defmodule DemoEventConsumer.HandlerTest do
  use ExUnit.Case, async: true

  alias DemoEventConsumer.Handler

  test "classifies poison reason as nak" do
    assert Handler.classify(%{"data" => %{"reason" => "poison", "service" => "x"}}) == :nak
  end

  test "classifies normal events as ack" do
    assert Handler.classify(%{"data" => %{"reason" => "oom", "service" => "api"}}) == :ack
  end

  test "process_message nacks poison via action" do
    msg = %{"event_id" => "e1", "ack_token" => "t1", "data" => %{"reason" => "poison"}}

    assert {:ok, :nacked} =
             Handler.process_message(msg, fn
               {:nak, ^msg} -> :ok
               other -> flunk("unexpected #{inspect(other)}")
             end)
  end

  test "process_message acks good events via action" do
    msg = %{"event_id" => "e2", "ack_token" => "t2", "data" => %{"reason" => "oom"}}

    assert {:ok, :acked} =
             Handler.process_message(msg, fn
               {:ack, ^msg} -> :ok
               other -> flunk("unexpected #{inspect(other)}")
             end)
  end
end
