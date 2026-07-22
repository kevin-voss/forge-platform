package forge.control.reconcile

import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class ReadinessGateTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")

    @Test
    fun returnsNotReadyUntilRuntimeReportsReady() {
        val runtime = GateFakeRuntime()
        val runtimeId = WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0)
        runtime.seed(runtimeId, "running", "registry.local/demo:v2")
        val gate = ReadinessGate(runtime, pollMs = 1, maxWaitSeconds = 1)

        assertFalse(gate.isReady(runtimeId))
        assertEquals(ReadinessOutcome.NotReady, gate.checkOnce(runtimeId).outcome)

        runtime.markStatus(runtimeId, "ready")
        assertTrue(gate.isReady(runtimeId))
        assertEquals(ReadinessOutcome.Ready, gate.checkOnce(runtimeId).outcome)
    }

    @Test
    fun awaitReadyTimesOutWhenNeverReady() {
        val runtime = GateFakeRuntime()
        val runtimeId = WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0)
        runtime.seed(runtimeId, "running", "registry.local/demo:v2")
        val sleeps = AtomicInteger(0)
        val gate = ReadinessGate(
            runtimeClient = runtime,
            pollMs = 1,
            maxWaitSeconds = 0,
            sleeper = { sleeps.incrementAndGet() },
        )

        val result = gate.awaitReady(runtimeId)
        assertEquals(ReadinessOutcome.TimedOut, result.outcome)
    }
}

private class GateFakeRuntime : RuntimeClient {
    private data class W(var status: String, var image: String)
    private val workloads = ConcurrentHashMap<String, W>()

    fun seed(runtimeId: String, status: String, image: String) {
        workloads[runtimeId] = W(status, image)
    }

    fun markStatus(runtimeId: String, status: String) {
        workloads.getValue(runtimeId).status = status
    }

    override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

    override fun observe(deploymentId: UUID): ActualState {
        val replicas = workloads.entries
            .filter { WorkloadNamer.matchesDeployment(it.key, deploymentId) }
            .map { (id, w) ->
                val index = WorkloadNamer.parseReplicaIndex(id)
                ReplicaObservation(
                    replicaId = index?.toString() ?: id,
                    status = w.status,
                    replicaIndex = index,
                    image = w.image,
                )
            }
        return ActualState(replicas)
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        val w = workloads[runtimeDeploymentId] ?: return null
        return WorkloadHandle(runtimeDeploymentId, w.status, image = w.image)
    }

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome = EnsureOutcome.Created

    override fun stopWorkload(runtimeDeploymentId: String) {
        workloads.remove(runtimeDeploymentId)
    }
}
