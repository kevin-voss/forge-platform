package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.TopologySpreadConstraint
import forge.control.scheduler.model.WhenUnsatisfiable
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class TopologySpreadTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")
    private val log = JsonLog("topology-spread-test", "error")
    private val serviceId = "svc_ha"

    private fun fourNodeTwoZoneFleet(): Triple<InMemoryNodeStore, CapacityReservation, InMemoryPlacementStore> {
        val nodes = InMemoryNodeStore()
        nodes.register(
            "node-a", "http://a", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-a", region = "default", provider = "docker"),
        )
        nodes.register(
            "node-b", "http://b", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-a", region = "default", provider = "docker"),
        )
        nodes.register(
            "node-c", "http://c", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-b", region = "default", provider = "docker"),
        )
        nodes.register(
            "node-d", "http://d", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-b", region = "default", provider = "docker"),
        )
        val placements = InMemoryPlacementStore()
        return Triple(nodes, CapacityReservation(nodes), placements)
    }

    private fun haConstraints() = listOf(
        TopologySpreadConstraint(
            topologyKey = "node",
            minimumDistinctNodes = 3,
            whenUnsatisfiable = "DoNotSchedule",
        ),
        TopologySpreadConstraint(
            topologyKey = "zone",
            minimumDistinctNodes = 2,
            whenUnsatisfiable = "DoNotSchedule",
        ),
    )

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
    fun threeReplicasLandOnDistinctNodesAcrossZones() {
        val (nodes, reservation, placements) = fourNodeTwoZoneFleet()
        val sched = scheduler(nodes, reservation, placements)
        val dpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
        val chosen = mutableListOf<String>()
        for (i in 0 until 3) {
            val decision = assertIs<PlacementDecision.Assigned>(
                sched.place(
                    PlacementRequest(
                        deploymentId = dpl.toString(),
                        replicaIndex = i,
                        serviceId = serviceId,
                        antiAffinity = AntiAffinity.Soft,
                        placement = PlacementSpec(topologySpreadConstraints = haConstraints()),
                    ),
                ),
            )
            chosen += decision.nodeId
            placements.upsert(
                Placement(
                    id = "plc_$i",
                    deploymentId = dpl,
                    replicaIndex = i,
                    nodeId = decision.nodeId,
                    strategy = decision.strategy,
                    reason = decision.reason,
                    createdAt = t0,
                    serviceId = serviceId,
                    topologySpreadConstraints = haConstraints(),
                    trace = decision.trace,
                ),
            )
        }
        assertEquals(3, chosen.toSet().size, "expect 3 distinct nodes, got $chosen")
        val zones = chosen.map { nodes.find(it)!!.zone }.toSet()
        assertEquals(2, zones.size, "expect both zones represented, got $zones from $chosen")
        assertTrue(placements.listPlaced().all { it.trace?.filters?.any { f -> f.name == "topology_spread" } == true })
    }

    @Test
    fun fourthPlacementPendingWhenOnlyThreeNodesFit() {
        val (nodes, reservation, placements) = fourNodeTwoZoneFleet()
        // Fill node-d so only 3 nodes have capacity.
        nodes.heartbeat("node-d", NodeAllocation(slots = 1), t0)
        val sched = scheduler(nodes, reservation, placements)
        val queue = PendingQueue(placements, maxLen = 10)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
        )
        val dpl = UUID.fromString("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
        for (i in 0 until 3) {
            assertIs<PlaceResult.Ok>(
                service.placeAndPersist(
                    deploymentId = dpl,
                    replicaIndex = i,
                    serviceId = serviceId,
                    placement = PlacementSpec(topologySpreadConstraints = haConstraints()),
                ),
            )
        }
        val fourth = assertIs<PlaceResult.Pending>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = 3,
                serviceId = serviceId,
                placement = PlacementSpec(topologySpreadConstraints = haConstraints()),
            ),
        )
        assertEquals("pending", fourth.placement.status)
        assertTrue(
            fourth.placement.reason!!.contains("TopologySpread") ||
                fourth.placement.reason!!.contains("no node") ||
                fourth.placement.reason!!.contains("anti-affinity") ||
                fourth.placement.trace?.filters?.any { it.name == "topology_spread" } == true,
        )
    }

    @Test
    fun spreadScorerPrefersEmptyZone() {
        val (nodes, _, placements) = fourNodeTwoZoneFleet()
        placements.upsert(
            Placement(
                id = "plc_0",
                deploymentId = UUID.fromString("cccccccc-cccc-cccc-cccc-cccccccccccc"),
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                serviceId = serviceId,
            ),
        )
        val scoreA = SpreadScorer.score(
            node = nodes.find("node-b")!!,
            serviceId = serviceId,
            constraints = listOf(TopologySpreadConstraint(topologyKey = "zone", minimumDistinctNodes = 2)),
            nodesById = nodes::find,
            placedForService = { placements.listPlacedByService(it) },
        )
        val scoreC = SpreadScorer.score(
            node = nodes.find("node-c")!!,
            serviceId = serviceId,
            constraints = listOf(TopologySpreadConstraint(topologyKey = "zone", minimumDistinctNodes = 2)),
            nodesById = nodes::find,
            placedForService = { placements.listPlacedByService(it) },
        )
        assertTrue(scoreC > scoreA, "empty zone should score higher ($scoreC vs $scoreA)")
    }

    @Test
    fun scheduleAnywayRelaxesWhenFleetTooSmall() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val placements = InMemoryPlacementStore()
        val reservation = CapacityReservation(nodes)
        val sched = scheduler(nodes, reservation, placements)
        val dpl = UUID.fromString("dddddddd-dddd-dddd-dddd-dddddddddddd")
        val constraints = listOf(
            TopologySpreadConstraint(
                topologyKey = "node",
                minimumDistinctNodes = 5,
                whenUnsatisfiable = WhenUnsatisfiable.ScheduleAnyway.wire(),
            ),
        )
        // Place two replicas; third would still not reach 5 distinct — must succeed with spread_relaxed.
        for (i in 0 until 2) {
            val decision = assertIs<PlacementDecision.Assigned>(
                sched.place(
                    PlacementRequest(
                        deploymentId = dpl.toString(),
                        replicaIndex = i,
                        serviceId = serviceId,
                        placement = PlacementSpec(topologySpreadConstraints = constraints),
                    ),
                ),
            )
            placements.upsert(
                Placement(
                    id = "plc_$i",
                    deploymentId = dpl,
                    replicaIndex = i,
                    nodeId = decision.nodeId,
                    strategy = decision.strategy,
                    reason = decision.reason,
                    createdAt = t0,
                    serviceId = serviceId,
                    topologySpreadConstraints = constraints,
                    trace = decision.trace,
                ),
            )
        }
        // After 2 distinct nodes, further placements reuse domains; ScheduleAnyway should relax.
        val third = assertIs<PlacementDecision.Assigned>(
            sched.place(
                PlacementRequest(
                    deploymentId = dpl.toString(),
                    replicaIndex = 2,
                    serviceId = serviceId,
                    placement = PlacementSpec(topologySpreadConstraints = constraints),
                ),
            ),
        )
        assertTrue(third.trace?.spreadRelaxed == true, "expected spread_relaxed in trace")
    }

    @Test
    fun rescheduleAfterNodeLossPreservesDistinctNodes() {
        val (nodes, reservation, placements) = fourNodeTwoZoneFleet()
        val sched = scheduler(nodes, reservation, placements)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = PendingQueue(placements, maxLen = 10),
        )
        val dpl = UUID.fromString("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
        val placedNodes = mutableListOf<String>()
        for (i in 0 until 3) {
            val ok = assertIs<PlaceResult.Ok>(
                service.placeAndPersist(
                    deploymentId = dpl,
                    replicaIndex = i,
                    serviceId = serviceId,
                    placement = PlacementSpec(topologySpreadConstraints = haConstraints()),
                ),
            )
            placedNodes += ok.placement.nodeId!!
        }
        val lostNode = placedNodes.first()
        val lostReplica = placements.listByDeployment(dpl).first { it.nodeId == lostNode }
        nodes.markOffline(lostNode)
        reservation.release(lostNode, forge.control.scheduler.model.ResourceRequirements(slots = 1))
        placements.markLost(dpl, lostReplica.replicaIndex)
        val replacement = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = lostReplica.replicaIndex,
                serviceId = serviceId,
                rescheduledFromNode = lostNode,
                placement = PlacementSpec(topologySpreadConstraints = haConstraints()),
            ),
        )
        val activeNodes = placements.listByDeployment(dpl, "placed").mapNotNull { it.nodeId }.toSet()
        assertEquals(3, activeNodes.size)
        assertTrue(lostNode !in activeNodes)
        assertTrue(replacement.placement.nodeId !in placedNodes - lostNode || replacement.placement.nodeId !in placedNodes)
        // Surviving replicas keep their nodes; replacement uses the previously idle 4th node.
        val survivors = placedNodes.filter { it != lostNode }.toSet()
        assertTrue(survivors.all { it in activeNodes })
        assertTrue(replacement.placement.nodeId !in survivors)
    }
}
