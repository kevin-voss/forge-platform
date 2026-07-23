package forge.control.scheduler

import forge.control.scheduler.model.NodeTaint
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.ResourceRequirements
import forge.control.scheduler.model.TaintEffect
import forge.control.scheduler.model.Toleration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class NodeSelectorFilterTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")

    @Test
    fun nodeLabelMergerNodeWinsConflictsAndKeepsDistinctKeys() {
        val result = NodeLabelMerger.merge(
            nodeId = "node-a",
            architecture = "arm64",
            os = "linux",
            provider = "hetzner",
            poolLabels = mapOf("tier" to "pool", "disk" to "hdd"),
            agentLabels = mapOf("tier" to "node", "zone" to "a"),
        )
        assertEquals("node", result.labels["tier"])
        assertEquals("hdd", result.labels["disk"])
        assertEquals("a", result.labels["zone"])
        assertEquals("node-a", result.labels[NodeLabelMerger.LABEL_NODE_ID])
        assertEquals("arm64", result.labels[NodeLabelMerger.LABEL_ARCH])
        assertEquals(1, result.conflicts.size)
        assertEquals("tier", result.conflicts.single().key)
    }

    @Test
    fun nodeSelectorEliminatesMissingLabel() {
        val store = InMemoryNodeStore()
        store.register(
            "node-a",
            "http://a",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(agentLabels = mapOf("disk" to "hdd")),
        )
        store.register(
            "node-b",
            "http://b",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(agentLabels = mapOf("disk" to "ssd")),
        )
        val scheduler = FirstFitScheduler(store, CapacityReservation(store))
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
                placement = PlacementSpec(nodeSelector = mapOf("disk" to "ssd")),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
        assertTrue(assigned.trace!!.filters.any { it.name == "node_selector" })
        assertTrue(
            assigned.trace!!.filters
                .first { it.name == "node_selector" }
                .eliminated
                .any { it.nodeId == "node-a" },
        )
    }

    @Test
    fun platformFilterSelectsArchitecture() {
        val store = InMemoryNodeStore()
        store.register(
            "node-amd",
            "http://a",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(architecture = "amd64"),
        )
        store.register(
            "node-arm",
            "http://b",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(architecture = "arm64"),
        )
        val scheduler = FirstFitScheduler(store, CapacityReservation(store))
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
                platform = PlatformSpec(architecture = "arm64"),
            ),
        )
        assertEquals("node-arm", assertIs<PlacementDecision.Assigned>(decision).nodeId)

        val omitted = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-2",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
            ),
        )
        // first-fit: node-amd first
        assertEquals("node-amd", assertIs<PlacementDecision.Assigned>(omitted).nodeId)
    }

    @Test
    fun taintBlocksWithoutTolerationAndExistsOperatorMatches() {
        val store = InMemoryNodeStore()
        store.register(
            "node-db",
            "http://a",
            NodeCapacity(slots = 4),
            t0,
            facts = NodeSchedulingFacts(
                taints = listOf(NodeTaint(key = "dedicated", value = "db", effect = "NoSchedule")),
            ),
        )
        store.register(
            "node-web",
            "http://b",
            NodeCapacity(slots = 4),
            t0,
        )
        val scheduler = FirstFitScheduler(store, CapacityReservation(store))
        val blocked = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-1",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
            ),
        )
        assertEquals("node-web", assertIs<PlacementDecision.Assigned>(blocked).nodeId)

        val withExists = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-2",
                replicaIndex = 0,
                requirements = ResourceRequirements(slots = 1, slotsExplicit = true),
                placement = PlacementSpec(
                    tolerations = listOf(
                        Toleration(key = "dedicated", operator = "Exists", effect = "NoSchedule"),
                    ),
                ),
            ),
        )
        assertEquals("node-db", assertIs<PlacementDecision.Assigned>(withExists).nodeId)
        assertEquals(TaintEffect.NoSchedule, TaintEffect.parse("NoSchedule"))
    }
}
