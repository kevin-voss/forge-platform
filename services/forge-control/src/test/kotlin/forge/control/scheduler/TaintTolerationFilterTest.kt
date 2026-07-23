package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.NodeTaint
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.ResourceRequirements
import forge.control.scheduler.model.Toleration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class TaintTolerationFilterTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")
    private val log = JsonLog("taint-test", "error")

    @Test
    fun noScheduleWithoutTolerationEliminatesNode() {
        val node = FleetNode(
            id = "n1",
            address = "http://n1",
            capacity = NodeCapacity(slots = 2),
            allocation = NodeAllocation(),
            status = "online",
            lastHeartbeatAt = t0,
            registeredAt = t0,
            taints = listOf(NodeTaint("gpu", "true", "NoSchedule")),
        )
        val result = TaintTolerationFilter.filter(listOf(node), emptyList())
        assertTrue(result.candidates.isEmpty())
        assertEquals("TaintNotTolerated", result.eliminated.single().reason)
    }

    @Test
    fun equalTolerationMatchesKeyValueEffect() {
        val taint = NodeTaint("dedicated", "db", "NoSchedule")
        val ok = Toleration(key = "dedicated", operator = "Equal", value = "db", effect = "NoSchedule")
        val bad = Toleration(key = "dedicated", operator = "equal", value = "web", effect = "NoSchedule")
        assertTrue(ok.matches(taint))
        assertTrue(!bad.matches(taint))
        assertTrue(
            Toleration(key = "dedicated", operator = "Exists").matches(taint),
        )
    }

    @Test
    fun noExecuteEvictsOnlyNonToleratingPlacementsOnThatNode() {
        val nodes = InMemoryNodeStore()
        nodes.register(
            "node-a",
            "http://a",
            NodeCapacity(slots = 4),
            t0,
        )
        nodes.register(
            "node-b",
            "http://b",
            NodeCapacity(slots = 4),
            t0,
        )
        val placements = InMemoryPlacementStore()
        val reservation = CapacityReservation(nodes)
        val scheduler = FirstFitScheduler(nodes, reservation)
        val service = PlacementService(
            scheduler = scheduler,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = PendingQueue(placements),
        )
        val dpl = UUID.fromString("11111111-1111-1111-1111-111111111111")
        // Place without toleration on node-a (first-fit).
        val placed = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(
                deploymentId = dpl,
                replicaIndex = 0,
                slots = 1,
            ),
        )
        assertEquals("node-a", placed.placement.nodeId)

        // Re-register node-a with NoExecute taint → eviction.
        val previous = nodes.find("node-a")!!.taints
        nodes.register(
            "node-a",
            "http://a",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(
                taints = listOf(NodeTaint("drain", "1", "NoExecute")),
            ),
        )
        val handler = TaintChangeHandler(
            store = placements,
            placementService = service,
            reservation = reservation,
            deploymentStore = null,
            log = log,
        )
        val evicted = handler.onTaintsChanged(
            "node-a",
            previous,
            nodes.find("node-a")!!.taints,
        )
        assertEquals(1, evicted)
        val lost = placements.listByDeployment(dpl, PendingQueue.STATUS_LOST)
        assertEquals(1, lost.size)
        assertEquals("node-a", lost.single().nodeId)
        // Replacement should land on node-b (node-a now tainted NoExecute).
        val active = placements.find(dpl, 0)
        assertEquals(PendingQueue.STATUS_PLACED, active!!.status)
        assertEquals("node-b", active.nodeId)
    }

    @Test
    fun legacyRequestUnchangedOnUntaintedFleet() {
        val store = InMemoryNodeStore()
        store.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        store.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val scheduler = FirstFitScheduler(store, CapacityReservation(store))
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
                placement = PlacementSpec(),
                platform = null,
            ),
        )
        assertEquals("node-a", assertIs<PlacementDecision.Assigned>(decision).nodeId)
        val filters = decision.trace!!.filterNames()
        assertEquals(
            listOf(
                "capacity",
                "node_selector",
                "platform",
                "taints",
                "workload_affinity",
                "topology_spread",
                "stateful",
            ),
            filters,
        )
    }
}
