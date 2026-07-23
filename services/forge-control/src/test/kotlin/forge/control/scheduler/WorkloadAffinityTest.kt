package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AffinityRules
import forge.control.scheduler.model.AffinitySelector
import forge.control.scheduler.model.AffinityTerm
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class WorkloadAffinityTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")
    private val log = JsonLog("workload-affinity-test", "error")

    private fun twoZoneFleet(): Triple<InMemoryNodeStore, CapacityReservation, InMemoryPlacementStore> {
        val nodes = InMemoryNodeStore()
        nodes.register(
            "node-a", "http://a", NodeCapacity(slots = 2), t0,
            facts = NodeSchedulingFacts(zone = "zone-a"),
        )
        nodes.register(
            "node-b", "http://b", NodeCapacity(slots = 2), t0,
            facts = NodeSchedulingFacts(zone = "zone-a"),
        )
        nodes.register(
            "node-c", "http://c", NodeCapacity(slots = 2), t0,
            facts = NodeSchedulingFacts(zone = "zone-b"),
        )
        val placements = InMemoryPlacementStore()
        return Triple(nodes, CapacityReservation(nodes), placements)
    }

    private fun scheduler(
        nodes: InMemoryNodeStore,
        reservation: CapacityReservation,
        placements: InMemoryPlacementStore,
    ) = LeastAllocatedScheduler(
        nodes = nodes,
        reservation = reservation,
        antiAffinity = AntiAffinityFilter(placements),
        workloadAffinity = WorkloadAffinityFilter(nodes, placements),
        topologySpread = TopologySpreadFilter(nodes, placements),
        placedReplicas = { placements.listPlaced() },
    )

    @Test
    fun requiredAffinityWithZeroMatchesIsPending() {
        val (nodes, reservation, placements) = twoZoneFleet()
        val sched = scheduler(nodes, reservation, placements)
        val queue = PendingQueue(placements, maxLen = 10)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
        )
        val dpl = UUID.fromString("11111111-2222-3333-4444-555555555555")
        val affinity = PlacementAffinity(
            workload = AffinityRules(
                requiredDuringScheduling = listOf(
                    AffinityTerm(
                        selector = AffinitySelector(service = "svc_cache"),
                        topologyKey = "zone",
                    ),
                ),
            ),
        )
        val result = assertIs<PlaceResult.Pending>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = 0,
                serviceId = "svc_api",
                placement = PlacementSpec(affinity = affinity),
            ),
        )
        assertEquals("pending", result.placement.status)
        assertTrue(
            result.placement.reason!!.contains("AffinityUnsatisfiable") ||
                result.placement.trace?.filters?.any { it.name == "workload_affinity" } == true,
        )
    }

    @Test
    fun requiredAffinityColocatesInMatchingZone() {
        val (nodes, reservation, placements) = twoZoneFleet()
        val sched = scheduler(nodes, reservation, placements)
        val cacheDpl = UUID.fromString("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
        // Place cache replica in zone-b (node-c).
        placements.upsert(
            Placement(
                id = "plc_cache",
                deploymentId = cacheDpl,
                replicaIndex = 0,
                nodeId = "node-c",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                serviceId = "svc_cache",
            ),
        )
        nodes.heartbeat("node-c", NodeAllocation(slots = 1), t0)

        val affinity = PlacementAffinity(
            workload = AffinityRules(
                requiredDuringScheduling = listOf(
                    AffinityTerm(
                        selector = AffinitySelector(service = "svc_cache"),
                        topologyKey = "zone",
                    ),
                ),
            ),
        )
        val decision = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = "ffffffff-ffff-ffff-ffff-ffffffffffff",
                    replicaIndex = 0,
                    serviceId = "svc_api",
                    placement = PlacementSpec(affinity = affinity),
                ),
            ),
        )
        assertEquals("zone-b", nodes.find(decision.nodeId)!!.zone)
        assertEquals("node-c", decision.nodeId)
    }

    @Test
    fun legacySoftAntiAffinityUnchanged() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val placements = InMemoryPlacementStore()
        val reservation = CapacityReservation(nodes)
        val sched = scheduler(nodes, reservation, placements)
        val dpl = UUID.fromString("99999999-9999-9999-9999-999999999999")
        val first = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 0,
                    serviceId = "svc_demo",
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
                serviceId = "svc_demo",
            ),
        )
        val second = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 1,
                    serviceId = "svc_demo",
                    antiAffinity = AntiAffinity.Soft,
                ),
            ),
        )
        assertTrue(second.nodeId != first.nodeId)
    }
}
