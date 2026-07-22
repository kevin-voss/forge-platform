package forge.control.scheduler

import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class NodeStoreTest {
    @Test
    fun registerIsIdempotentByNodeId() {
        val store = InMemoryNodeStore()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        val first = store.register(
            id = "node-a",
            address = "http://runtime-a:4102",
            capacity = NodeCapacity(slots = 4, cpuMillis = 4000, memMb = 4096),
            at = t0,
        )
        val again = store.register(
            id = "node-a",
            address = "http://runtime-a:4102",
            capacity = NodeCapacity(slots = 8),
            at = t0.plusSeconds(5),
        )
        assertEquals(first.id, again.id)
        assertEquals(8, again.capacity.slots)
        assertEquals("online", again.status)
        assertEquals(1, store.list().size)
    }

    @Test
    fun heartbeatUpdatesAllocationAndTimestamp() {
        val store = InMemoryNodeStore()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        store.register("node-a", "http://runtime-a:4102", NodeCapacity(slots = 4), t0)
        val hb = store.heartbeat(
            "node-a",
            NodeAllocation(slots = 2, runningReplicas = listOf("dpl_1:0", "dpl_1:1")),
            t0.plusSeconds(3),
        )
        assertNotNull(hb)
        assertEquals(2, hb.allocation.slots)
        assertEquals(listOf("dpl_1:0", "dpl_1:1"), hb.allocation.runningReplicas)
        assertEquals(t0.plusSeconds(3), hb.lastHeartbeatAt)
        assertEquals("online", hb.status)
    }

    @Test
    fun heartbeatUnknownNodeReturnsNull() {
        val store = InMemoryNodeStore()
        assertNull(
            store.heartbeat(
                "missing",
                NodeAllocation(slots = 0),
                Instant.parse("2026-07-22T12:00:00Z"),
            ),
        )
    }

    @Test
    fun listReflectsStatusAndFreeCapacity() {
        val store = InMemoryNodeStore()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        store.heartbeat("node-a", NodeAllocation(slots = 1), t0.plusSeconds(1))
        store.markStaleOffline(t0.plusSeconds(1)) // node-b still at t0 → offline

        val listed = store.list()
        assertEquals(2, listed.size)
        val a = listed.first { it.id == "node-a" }
        val b = listed.first { it.id == "node-b" }
        assertEquals("online", a.status)
        assertEquals(3, LivenessMonitor.freeSlots(a))
        assertEquals("offline", b.status)
    }
}
