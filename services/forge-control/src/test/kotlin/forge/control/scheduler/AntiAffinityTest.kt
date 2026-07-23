package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class AntiAffinityTest {
    private val t0 = Instant.parse("2026-07-22T12:00:00Z")
    private val log = JsonLog("anti-affinity-test", "error")
    private val serviceId = "svc_demo"

    private fun twoNodeFleet(aSlots: Int = 2, bSlots: Int = 2): Triple<InMemoryNodeStore, CapacityReservation, InMemoryPlacementStore> {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = aSlots), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = bSlots), t0)
        val placements = InMemoryPlacementStore()
        return Triple(nodes, CapacityReservation(nodes), placements)
    }

    private fun scheduler(
        nodes: InMemoryNodeStore,
        reservation: CapacityReservation,
        placements: InMemoryPlacementStore,
    ): LeastAllocatedScheduler =
        LeastAllocatedScheduler(
            nodes = nodes,
            reservation = reservation,
            antiAffinity = AntiAffinityFilter(placements),
            workloadAffinity = WorkloadAffinityFilter(nodes, placements),
            topologySpread = TopologySpreadFilter(nodes, placements),
            placedReplicas = { placements.listPlaced() },
        )

    @Test
    fun softPrefersDistinctNodeWhenBothHaveCapacity() {
        val (nodes, reservation, placements) = twoNodeFleet()
        val sched = scheduler(nodes, reservation, placements)
        val dpl = UUID.fromString("11111111-1111-1111-1111-111111111111")

        val first = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 0,
                    serviceId = serviceId,
                    antiAffinity = AntiAffinity.Soft,
                ),
            ),
        )
        placements.upsert(
            Placement(
                id = "plc_0",
                deploymentId = dpl,
                replicaIndex = 0,
                nodeId = first.nodeId,
                strategy = first.strategy,
                reason = first.reason,
                createdAt = t0,
                serviceId = serviceId,
            ),
        )

        val second = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 1,
                    serviceId = serviceId,
                    antiAffinity = AntiAffinity.Soft,
                ),
            ),
        )
        assertTrue(second.nodeId != first.nodeId, "soft anti-affinity should spread across nodes")
    }

    @Test
    fun softFallsBackToColocationWhenOnlyOneNodeFits() {
        val (nodes, reservation, placements) = twoNodeFleet(aSlots = 2, bSlots = 1)
        // Fill node-b completely so only node-a has capacity.
        nodes.heartbeat("node-b", NodeAllocation(slots = 1), t0)
        val sched = scheduler(nodes, reservation, placements)
        val dpl = UUID.fromString("22222222-2222-2222-2222-222222222222")

        val first = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 0,
                    serviceId = serviceId,
                    antiAffinity = AntiAffinity.Soft,
                ),
            ),
        )
        assertEquals("node-a", first.nodeId)
        placements.upsert(
            Placement(
                id = "plc_0",
                deploymentId = dpl,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = first.strategy,
                reason = first.reason,
                createdAt = t0,
                serviceId = serviceId,
            ),
        )

        val second = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 1,
                    serviceId = serviceId,
                    antiAffinity = AntiAffinity.Soft,
                ),
            ),
        )
        assertEquals("node-a", second.nodeId, "soft should co-locate when no distinct node fits")
    }

    @Test
    fun hardEnqueuesInsteadOfColocating() {
        val (nodes, reservation, placements) = twoNodeFleet(aSlots = 2, bSlots = 1)
        nodes.heartbeat("node-b", NodeAllocation(slots = 1), t0)
        val sched = scheduler(nodes, reservation, placements)
        val queue = PendingQueue(placements, maxLen = 10)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
        )
        val dpl = UUID.fromString("33333333-3333-3333-3333-333333333333")

        val first = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = 0,
                serviceId = serviceId,
                antiAffinity = AntiAffinity.Hard,
            ),
        )
        assertEquals("node-a", first.placement.nodeId)

        val second = assertIs<PlaceResult.Pending>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = 1,
                serviceId = serviceId,
                antiAffinity = AntiAffinity.Hard,
            ),
        )
        assertEquals("pending", second.placement.status)
        assertTrue(second.placement.reason!!.contains("anti-affinity"))
        assertEquals(null, second.placement.nodeId)
        assertEquals(1, nodes.find("node-a")!!.allocation.slots)
    }
}
