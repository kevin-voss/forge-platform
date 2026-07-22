package forge.control.scheduler

import forge.control.logging.JsonLog
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals

class LivenessMonitorTest {
    @Test
    fun marksOfflineExactlyWhenHeartbeatOlderThanTimeout() {
        val store = InMemoryNodeStore()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        store.register(
            id = "node-a",
            address = "http://runtime-a:4102",
            capacity = NodeCapacity(slots = 4),
            at = t0,
        )

        val clock = mutableClock(t0)
        val monitor = LivenessMonitor(
            store = store,
            timeout = Duration.ofSeconds(15),
            intervalMs = 60_000,
            log = JsonLog("test", "error"),
            clock = clock,
        )

        // Exactly at timeout boundary: age == 15s → still online (strictly older).
        clock.set(t0.plusSeconds(15))
        monitor.evaluate()
        assertEquals("online", store.find("node-a")!!.status)

        // One nanosecond past timeout → offline.
        clock.set(t0.plusSeconds(15).plusNanos(1))
        monitor.evaluate()
        assertEquals("offline", store.find("node-a")!!.status)
        assertEquals(t0, store.find("node-a")!!.lastHeartbeatAt)
    }

    @Test
    fun freshHeartbeatBringsNodeBackOnlineOnRecompute() {
        val store = InMemoryNodeStore()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        store.register("node-b", "http://runtime-b:4102", NodeCapacity(slots = 4), t0)
        val clock = mutableClock(t0.plusSeconds(30))
        val monitor = LivenessMonitor(
            store = store,
            timeout = Duration.ofSeconds(15),
            intervalMs = 60_000,
            log = JsonLog("test", "error"),
            clock = clock,
        )
        monitor.evaluate()
        assertEquals("offline", store.find("node-b")!!.status)

        store.heartbeat(
            "node-b",
            NodeAllocation(slots = 1, runningReplicas = listOf("dpl:0")),
            t0.plusSeconds(30),
        )
        monitor.evaluate()
        assertEquals("online", store.find("node-b")!!.status)
        assertEquals(1, store.find("node-b")!!.allocation.slots)
    }

    private fun mutableClock(initial: Instant): MutableClock = MutableClock(initial)

    private class MutableClock(private var instant: Instant) : Clock() {
        fun set(next: Instant) {
            instant = next
        }

        override fun getZone() = ZoneOffset.UTC
        override fun withZone(zone: java.time.ZoneId?) = this
        override fun instant() = instant
    }
}
