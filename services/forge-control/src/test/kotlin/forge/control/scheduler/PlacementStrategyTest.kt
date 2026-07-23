package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class PlacementStrategyTest {
    private val t0 = Instant.parse("2026-07-22T12:00:00Z")

    private fun fleetA1B4(): Pair<InMemoryNodeStore, CapacityReservation> {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 5), t0)
        // free: a=1, b=4
        store.heartbeat("node-a", NodeAllocation(slots = 1), t0)
        store.heartbeat("node-b", NodeAllocation(slots = 1), t0)
        return store to CapacityReservation(store)
    }

    @Test
    fun firstFitPicksLowestIdThatFits() {
        val (store, reservation) = fleetA1B4()
        val scheduler = FirstFitScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-a", assigned.nodeId)
        assertEquals(FirstFitScheduler.STRATEGY, assigned.strategy)
        assertTrue(assigned.reason.contains("first-fit"))
        assertEquals(2, store.find("node-a")!!.allocation.slots)
    }

    @Test
    fun leastAllocatedPicksMostFreeSlots() {
        val (store, reservation) = fleetA1B4()
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
        assertEquals(LeastAllocatedScheduler.STRATEGY, assigned.strategy)
        assertTrue(assigned.reason.contains("least-allocated"))
        assertTrue(assigned.reason.contains("free=4"))
    }

    @Test
    fun leastAllocatedTieBreaksByLowestId() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        val reservation = CapacityReservation(store)
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-a", assigned.nodeId)
    }

    @Test
    fun capacityGateSkipsNodesWithoutEnoughFreeSlots() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        store.heartbeat("node-a", NodeAllocation(slots = 2), t0) // free 0
        store.heartbeat("node-b", NodeAllocation(slots = 1), t0) // free 3
        val reservation = CapacityReservation(store)
        val firstFit = FirstFitScheduler(store, reservation)
        val decision = firstFit.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 2),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
    }

    @Test
    fun offlineNodesAreNeverChosen() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        store.markStaleOffline(t0.plusSeconds(1)) // both heartbeat at t0 → offline
        // bring a back online with fresh heartbeat
        store.heartbeat("node-a", NodeAllocation(slots = 0), t0.plusSeconds(2))
        val reservation = CapacityReservation(store)
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-a", assigned.nodeId)
    }

    @Test
    fun overCommitSecondPlacementGetsOtherNodeOrNoNode() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 1), t0)
        val reservation = CapacityReservation(store)
        val scheduler = FirstFitScheduler(store, reservation)
        val first = assertIs<PlacementDecision.Assigned>(
            scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0)),
        )
        assertEquals("node-a", first.nodeId)
        val second = assertIs<PlacementDecision.Assigned>(
            scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = 1)),
        )
        assertEquals("node-b", second.nodeId)
        val third = scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = 2))
        assertIs<PlacementDecision.NoNodeAvailable>(third)
    }

    @Test
    fun leastAllocatedDistributesFourReplicasEvenly() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        val reservation = CapacityReservation(store)
        val scheduler = LeastAllocatedScheduler(store, reservation)
        val counts = mutableMapOf<String, Int>()
        repeat(4) { i ->
            val decision = assertIs<PlacementDecision.Assigned>(
                scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = i)),
            )
            counts[decision.nodeId] = (counts[decision.nodeId] ?: 0) + 1
        }
        assertEquals(2, counts["node-a"])
        assertEquals(2, counts["node-b"])
        assertEquals(2, store.find("node-a")!!.allocation.slots)
        assertEquals(2, store.find("node-b")!!.allocation.slots)
    }

    @Test
    fun firstFitFillsStableOrderBeforeSecondNode() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        val reservation = CapacityReservation(store)
        val scheduler = FirstFitScheduler(store, reservation)
        val nodes = (0 until 4).map { i ->
            assertIs<PlacementDecision.Assigned>(
                scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = i)),
            ).nodeId
        }
        assertEquals(listOf("node-a", "node-a", "node-a", "node-a"), nodes)
        val fifth = assertIs<PlacementDecision.Assigned>(
            scheduler.place(PlacementRequest(deploymentId = "dpl-1", replicaIndex = 4)),
        )
        assertEquals("node-b", fifth.nodeId)
    }

    @Test
    fun factorySelectsStrategiesAndReleaseHookWorks() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        val reservation = CapacityReservation(store)
        val least = SchedulerFactory.create(
            strategy = "least-allocated",
            nodeStore = store,
            reservation = reservation,
            localNodeId = "node-local",
            schedulerEnabled = true,
        )
        assertIs<LeastAllocatedScheduler>(least)
        val first = SchedulerFactory.create(
            strategy = "first-fit",
            nodeStore = store,
            reservation = reservation,
            localNodeId = "node-local",
            schedulerEnabled = true,
        )
        assertIs<FirstFitScheduler>(first)

        val placementStore = InMemoryPlacementStore()
        val service = PlacementService(
            scheduler = least,
            store = placementStore,
            log = forge.control.logging.JsonLog("test", "error"),
            reservation = reservation,
        )
        val dpl = UUID.fromString("11111111-1111-1111-1111-111111111111")
        val ok = assertIs<PlaceResult.Ok>(service.placeAndPersist(dpl, 0))
        assertEquals(1, store.find("node-a")!!.allocation.slots)
        service.releasePlacement(dpl, 0)
        assertEquals(0, store.find("node-a")!!.allocation.slots)
        assertEquals(null, placementStore.find(dpl, 0))
        assertEquals("least-allocated", ok.placement.strategy)
    }

    @Test
    fun releaseOrphanedAboveDesiredFreesReservedSlots() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        val reservation = CapacityReservation(store)
        val service = PlacementService(
            scheduler = LeastAllocatedScheduler(store, reservation),
            store = InMemoryPlacementStore(),
            log = forge.control.logging.JsonLog("test", "error"),
            reservation = reservation,
        )
        val dpl = UUID.fromString("22222222-2222-2222-2222-222222222222")
        repeat(4) { idx ->
            assertIs<PlaceResult.Ok>(service.placeAndPersist(dpl, idx))
        }
        assertEquals(4, store.find("node-a")!!.allocation.slots)

        val released = service.releaseOrphanedAboveDesired(dpl, desiredReplicas = 2)
        assertEquals(2, released)
        assertEquals(2, store.find("node-a")!!.allocation.slots)
        assertEquals(null, service.list(dpl).find { it.replicaIndex == 2 })
        assertEquals(null, service.list(dpl).find { it.replicaIndex == 3 })
        assertEquals(2, service.list(dpl).size)
    }
}
