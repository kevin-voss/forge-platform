package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.ResourceRequirements
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class CapacityAccountingTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")

    @Test
    fun overcommitIncreasesAllocatableAndReservedIsSubtracted() {
        val capacity = NodeCapacity(slots = 4, cpuMillis = 4000, memMb = 4096, diskMb = 10240)
        val cfg = OvercommitConfig(
            cpuRatio = 2.0,
            memoryRatio = 1.5,
            systemReservedCpuMillis = 100,
            systemReservedMemoryMb = 256,
            systemReservedDiskMb = 512,
        )
        val alloc = CapacityAccounting.allocatable(capacity, NodeReserved(), cfg)
        assertEquals(4, alloc.slots)
        assertEquals(7900, alloc.cpuMillis) // 4000*2 - 100
        assertEquals(5888, alloc.memMb) // 4096*1.5 - 256
        assertEquals(9728, alloc.diskMb) // 10240 - 512 (never overcommitted)
    }

    @Test
    fun capacityFilterEliminatesInsufficientCpuAndRecordsTrace() {
        val store = InMemoryNodeStore(
            OvercommitConfig(
                systemReservedCpuMillis = 0,
                systemReservedMemoryMb = 0,
            ),
        )
        store.register(
            "node-a",
            "http://a",
            NodeCapacity(slots = 4, cpuMillis = 1000, memMb = 4096),
            t0,
        )
        store.register(
            "node-b",
            "http://b",
            NodeCapacity(slots = 4, cpuMillis = 4000, memMb = 4096),
            t0,
        )
        store.heartbeat("node-a", NodeAllocation(slots = 0, cpuMillis = 600, memMb = 0), t0)
        val reservation = CapacityReservation(store)
        val scheduler = FirstFitScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(
                    requests = ResourceBundle(cpuMillis = 500, memoryMb = 512),
                ),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
        val trace = assertNotNull(assigned.trace)
        assertEquals("capacity", trace.filters.single().name)
        assertTrue(trace.filters.single().eliminated.any { it.reason == "InsufficientCPU" })
    }

    @Test
    fun exceedingAllNodesPendingWithUnschedulableReasons() {
        val store = InMemoryNodeStore(
            OvercommitConfig(
                systemReservedCpuMillis = 0,
                systemReservedMemoryMb = 0,
            ),
        )
        store.register(
            "node-a",
            "http://a",
            NodeCapacity(slots = 4, cpuMillis = 500, memMb = 256),
            t0,
        )
        store.register(
            "node-b",
            "http://b",
            NodeCapacity(slots = 4, cpuMillis = 500, memMb = 256),
            t0,
        )
        val reservation = CapacityReservation(store)
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(
                    requests = ResourceBundle(cpuMillis = 1000, memoryMb = 512),
                ),
            ),
        )
        val pending = assertIs<PlacementDecision.NoNodeAvailable>(decision)
        assertTrue(pending.unschedulableReasons.isNotEmpty())
        assertTrue(pending.unschedulableReasons.all { it.nodeId in setOf("node-a", "node-b") })
        assertNotNull(pending.trace)
        assertEquals("capacity", pending.trace!!.filters.single().name)
    }

    @Test
    fun legacySlotsOnlyMatchesEpic08Outcome() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 5), t0)
        store.heartbeat("node-a", NodeAllocation(slots = 1), t0)
        store.heartbeat("node-b", NodeAllocation(slots = 1), t0)
        val reservation = CapacityReservation(store)
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
    }
}
