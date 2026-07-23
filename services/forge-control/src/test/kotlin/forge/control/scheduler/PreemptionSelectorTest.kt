package forge.control.scheduler

import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.TopologySpreadConstraint
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PreemptionSelectorTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")

    private fun setup(): Setup {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 1), t0)
        val placements = InMemoryPlacementStore()
        val priorities = InMemoryPriorityClassStore()
        priorities.create("high", 100, PreemptionPolicy.PreemptLowerPriority, "critical")
        priorities.create("low", -10, PreemptionPolicy.Never, "batch")
        val budgets = InMemoryDisruptionBudgetStore()
        val guard = DisruptionBudgetGuard(budgets, placements)
        val reservation = CapacityReservation(nodes)
        val selector = PreemptionSelector(
            nodes = nodes,
            placements = placements,
            priorityClasses = priorities,
            budgetGuard = guard,
        )
        return Setup(nodes, placements, priorities, budgets, reservation, selector)
    }

    @Test
    fun picksSmallestLowerPriorityVictimSet() {
        val s = setup()
        val lowDpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
        val highDpl = UUID.fromString("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
        // Fill both nodes with low-priority placements.
        for ((node, idx) in listOf("node-a" to 0, "node-b" to 1)) {
            s.reservation.tryReserve(node, forge.control.scheduler.model.ResourceRequirements(slots = 1))
            s.placements.upsert(
                Placement(
                    id = "plc_low_$idx",
                    deploymentId = lowDpl,
                    replicaIndex = idx,
                    nodeId = node,
                    strategy = "first-fit",
                    reason = "seed",
                    createdAt = t0,
                    slots = 1,
                    priorityClass = "low",
                ),
            )
        }
        val selection = s.selector.findMinimalVictims(
            PlacementRequest(
                deploymentId = highDpl.toString(),
                replicaIndex = 0,
                serviceId = "svc_critical",
                priorityClass = "high",
            ),
            s.priorities.resolve("high"),
        )
        assertNotNull(selection)
        assertEquals(1, selection.victims.size)
        assertTrue(selection.victims.all { it.priorityClass == "low" })
        assertTrue(selection.nodeId in setOf("node-a", "node-b"))
    }

    @Test
    fun neverSelectsEqualOrHigherPriorityVictim() {
        val s = setup()
        val peerDpl = UUID.fromString("cccccccc-cccc-cccc-cccc-cccccccccccc")
        val highDpl = UUID.fromString("dddddddd-dddd-dddd-dddd-dddddddddddd")
        s.reservation.tryReserve("node-a", forge.control.scheduler.model.ResourceRequirements(slots = 1))
        s.placements.upsert(
            Placement(
                id = "plc_peer",
                deploymentId = peerDpl,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                slots = 1,
                priorityClass = "high",
            ),
        )
        s.reservation.tryReserve("node-b", forge.control.scheduler.model.ResourceRequirements(slots = 1))
        s.placements.upsert(
            Placement(
                id = "plc_peer2",
                deploymentId = peerDpl,
                replicaIndex = 1,
                nodeId = "node-b",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                slots = 1,
                priorityClass = "high",
            ),
        )
        val selection = s.selector.findMinimalVictims(
            PlacementRequest(
                deploymentId = highDpl.toString(),
                replicaIndex = 0,
                priorityClass = "high",
            ),
            s.priorities.resolve("high"),
        )
        assertNull(selection)
    }

    @Test
    fun respectsTopologySpreadForPreemptor() {
        val nodes = InMemoryNodeStore()
        nodes.register(
            "node-a", "http://a", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-a"),
        )
        nodes.register(
            "node-b", "http://b", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-a"),
        )
        nodes.register(
            "node-c", "http://c", NodeCapacity(slots = 1), t0,
            facts = NodeSchedulingFacts(zone = "zone-b"),
        )
        val placements = InMemoryPlacementStore()
        val priorities = InMemoryPriorityClassStore()
        priorities.create("high", 100, PreemptionPolicy.PreemptLowerPriority)
        priorities.create("low", -10, PreemptionPolicy.Never)
        val reservation = CapacityReservation(nodes)
        // Place two HA replicas on a and b; fill c with low-priority filler.
        val ha = UUID.fromString("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
        for ((node, idx) in listOf("node-a" to 0, "node-b" to 1)) {
            reservation.tryReserve(node, forge.control.scheduler.model.ResourceRequirements(slots = 1))
            placements.upsert(
                Placement(
                    id = "plc_ha_$idx",
                    deploymentId = ha,
                    replicaIndex = idx,
                    nodeId = node,
                    strategy = "least-allocated",
                    reason = "seed",
                    createdAt = t0,
                    serviceId = "svc_ha",
                    priorityClass = "high",
                ),
            )
        }
        val low = UUID.fromString("ffffffff-ffff-ffff-ffff-ffffffffffff")
        reservation.tryReserve("node-c", forge.control.scheduler.model.ResourceRequirements(slots = 1))
        placements.upsert(
            Placement(
                id = "plc_filler",
                deploymentId = low,
                replicaIndex = 0,
                nodeId = "node-c",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                priorityClass = "low",
            ),
        )
        val selector = PreemptionSelector(
            nodes = nodes,
            placements = placements,
            priorityClasses = priorities,
            workloadAffinity = WorkloadAffinityFilter(nodes, placements),
            topologySpread = TopologySpreadFilter(nodes, placements),
        )
        // Third HA replica needs distinct node (>=3) — only node-c is eligible after preemption.
        val selection = selector.findMinimalVictims(
            PlacementRequest(
                deploymentId = ha.toString(),
                replicaIndex = 2,
                serviceId = "svc_ha",
                antiAffinity = AntiAffinity.Soft,
                placement = PlacementSpec(
                    topologySpreadConstraints = listOf(
                        TopologySpreadConstraint(
                            topologyKey = "node",
                            minimumDistinctNodes = 3,
                            whenUnsatisfiable = "DoNotSchedule",
                        ),
                    ),
                ),
                priorityClass = "high",
            ),
            priorities.resolve("high"),
        )
        assertNotNull(selection)
        assertEquals("node-c", selection.nodeId)
        assertEquals(listOf("plc_filler"), selection.victims.map { it.id })
    }

    @Test
    fun budgetBlocksOnlyViableVictim() {
        val s = setup()
        val lowDpl = UUID.fromString("11111111-1111-1111-1111-111111111111")
        val highDpl = UUID.fromString("22222222-2222-2222-2222-222222222222")
        s.reservation.tryReserve("node-a", forge.control.scheduler.model.ResourceRequirements(slots = 1))
        s.placements.upsert(
            Placement(
                id = "plc_last",
                deploymentId = lowDpl,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                priorityClass = "low",
            ),
        )
        s.reservation.tryReserve("node-b", forge.control.scheduler.model.ResourceRequirements(slots = 1))
        s.placements.upsert(
            Placement(
                id = "plc_last_b",
                deploymentId = lowDpl,
                replicaIndex = 1,
                nodeId = "node-b",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                priorityClass = "low",
            ),
        )
        // min_available = current running (2) blocks any voluntary removal.
        s.budgets.upsert(DisruptionBudget(deploymentId = lowDpl, minAvailable = 2, createdAt = t0))
        val selection = s.selector.findMinimalVictims(
            PlacementRequest(
                deploymentId = highDpl.toString(),
                replicaIndex = 0,
                priorityClass = "high",
            ),
            s.priorities.resolve("high"),
        )
        assertNull(selection)
    }

    private data class Setup(
        val nodes: InMemoryNodeStore,
        val placements: InMemoryPlacementStore,
        val priorities: InMemoryPriorityClassStore,
        val budgets: InMemoryDisruptionBudgetStore,
        val reservation: CapacityReservation,
        val selector: PreemptionSelector,
    )
}
