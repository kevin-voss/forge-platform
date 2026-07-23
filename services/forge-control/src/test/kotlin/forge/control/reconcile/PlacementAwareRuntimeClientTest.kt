package forge.control.reconcile

import forge.control.scheduler.InMemoryNodeStore
import forge.control.scheduler.InMemoryPlacementStore
import forge.control.scheduler.NodeCapacity
import forge.control.scheduler.PendingQueue
import forge.control.scheduler.Placement
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class PlacementAwareRuntimeClientTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")

    @Test
    fun observeMergesReplicasFromPlacedNodeAgents() {
        val deploymentId = UUID.randomUUID()
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://runtime-a:8080", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://runtime-b:8080", NodeCapacity(slots = 2), t0)
        val placements = InMemoryPlacementStore()
        placements.upsert(
            Placement(
                id = "p0",
                deploymentId = deploymentId,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                status = PendingQueue.STATUS_PLACED,
            ),
        )
        placements.upsert(
            Placement(
                id = "p1",
                deploymentId = deploymentId,
                replicaIndex = 1,
                nodeId = "node-b",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                status = PendingQueue.STATUS_PLACED,
            ),
        )

        val agents = mapOf(
            "http://runtime-a:8080" to RecordingRuntime("a", listOf(0)),
            "http://runtime-b:8080" to RecordingRuntime("b", listOf(1)),
        )
        val fallback = RecordingRuntime("fallback", emptyList())
        val client = PlacementAwareRuntimeClient(
            fallback = fallback,
            nodeStore = nodes,
            placementStore = placements,
            clientFactory = { address -> agents.getValue(address) },
        )

        val actual = client.observe(deploymentId)
        assertEquals(setOf(0, 1), actual.replicas.mapNotNull { it.replicaIndex }.toSet())
        assertTrue(fallback.observeCalls == 1)
        assertEquals(1, agents.getValue("http://runtime-a:8080").observeCalls)
        assertEquals(1, agents.getValue("http://runtime-b:8080").observeCalls)
    }

    @Test
    fun observeSurvivesMissingFallbackWhenPlacedNodesReport() {
        val deploymentId = UUID.randomUUID()
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://runtime-a:8080", NodeCapacity(slots = 2), t0)
        val placements = InMemoryPlacementStore()
        placements.upsert(
            Placement(
                id = "p0",
                deploymentId = deploymentId,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                status = PendingQueue.STATUS_PLACED,
            ),
        )
        val agents = mapOf("http://runtime-a:8080" to RecordingRuntime("a", listOf(0)))
        val client = PlacementAwareRuntimeClient(
            fallback = object : RuntimeClient {
                override fun loadActual(deploymentId: UUID) = observe(deploymentId)
                override fun observe(deploymentId: UUID): ActualState {
                    throw RuntimeUnreachableException("seed runtime down")
                }

                override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? = null
                override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome =
                    EnsureOutcome.Created

                override fun drainWorkload(runtimeDeploymentId: String) = Unit
                override fun stopWorkload(runtimeDeploymentId: String) = Unit
                override fun listWorkloads(): List<WorkloadHandle> = emptyList()
            },
            nodeStore = nodes,
            placementStore = placements,
            clientFactory = { address -> agents.getValue(address) },
        )

        val actual = client.observe(deploymentId)
        assertEquals(listOf(0), actual.replicas.mapNotNull { it.replicaIndex })
    }

    private class RecordingRuntime(
        private val name: String,
        private val indexes: List<Int>,
    ) : RuntimeClient {
        var observeCalls = 0

        override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

        override fun observe(deploymentId: UUID): ActualState {
            observeCalls += 1
            return ActualState(
                indexes.map { idx ->
                    ReplicaObservation(
                        replicaId = "$name-$idx",
                        status = "ready",
                        replicaIndex = idx,
                        restartCount = 0,
                        workloadName = "forge-$name-$idx",
                        image = "demo:v1",
                    )
                },
            )
        }

        override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? = null

        override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome =
            EnsureOutcome.Created

        override fun drainWorkload(runtimeDeploymentId: String) = Unit

        override fun stopWorkload(runtimeDeploymentId: String) = Unit

        override fun listWorkloads(): List<WorkloadHandle> = emptyList()
    }
}
