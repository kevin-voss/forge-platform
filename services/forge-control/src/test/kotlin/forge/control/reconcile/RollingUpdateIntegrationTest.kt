package forge.control.reconcile

import forge.control.logging.JsonLog
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class RollingUpdateIntegrationTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val log = JsonLog("forge-control-test", "error")
    private val v1 = "registry.local/demo:v1"
    private val v2 = "registry.local/demo:v2"

    @Test
    fun rolloutKeepsReadyReplicasAtLeastOne() {
        val runtime = RollingFakeRuntime(autoReady = true)
        val gateway = RecordingGatewayClient()
        // Seed two v1 replicas
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v1)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v1)

        val store = RollingFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v2,
                    replicas = 2,
                    batchSize = 1,
                    serviceSlug = "demo",
                ),
            ),
        )
        val status = InMemoryReconcileStatusStore()
        val minReadyObserved = AtomicInteger(Int.MAX_VALUE)
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = object : RuntimeClient by runtime {
                override fun observe(deploymentId: UUID): ActualState {
                    val actual = runtime.observe(deploymentId)
                    val ready = actual.replicas.count { it.statusEnum() == ReplicaStatus.Ready }
                    minReadyObserved.updateAndGet { minOf(it, ready) }
                    return actual
                }
            },
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            maxActionsPerTick = 10,
            trafficShifter = TrafficShifter(gateway),
            readinessMaxWaitSeconds = 30,
        )

        // Drive until converged or safety bound
        for (i in 1..20) {
            controller.tickAll()
            val actual = runtime.observe(deploymentId)
            val allV2 = actual.replicas.isNotEmpty() &&
                actual.replicas.all { it.image == v2 } &&
                actual.replicas.size == 2
            if (allV2) break
        }

        val final = runtime.observe(deploymentId)
        assertEquals(2, final.replicas.size)
        assertTrue(final.replicas.all { it.image == v2 })
        assertTrue(minReadyObserved.get() >= 1, "ready dipped to ${minReadyObserved.get()}")
        assertTrue(gateway.refreshCalls.get() > 0)
        val snap = status.findByDeploymentId(deploymentId)!!
        assertEquals(RolloutPhase.Idle.wire(), snap.plan.phase)
        assertEquals("2/2", "${snap.plan.updatedReplicas}/${snap.plan.totalReplicas}")
    }

    @Test
    fun neverReadyHoldsRolloutWithoutStoppingOld() {
        val runtime = RollingFakeRuntime(autoReady = false)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v1)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v1)

        val store = RollingFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v2,
                    replicas = 2,
                    batchSize = 1,
                    serviceSlug = "demo",
                ),
            ),
        )
        val status = InMemoryReconcileStatusStore()
        val gateway = RecordingGatewayClient()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            maxActionsPerTick = 10,
            trafficShifter = TrafficShifter(gateway),
            readinessMaxWaitSeconds = 1,
        )

        repeat(5) { controller.tickAll() }

        assertEquals(0, runtime.stopCalls.get())
        val actual = runtime.observe(deploymentId)
        assertTrue(actual.replicas.any { it.image == v1 && it.statusEnum() == ReplicaStatus.Ready })
        // Surge replica may exist but old must remain
        assertTrue(actual.replicas.count { it.image == v1 } >= 2 || actual.replicas.count { it.image == v1 } == 2)
        assertEquals(2, actual.replicas.count { it.image == v1 })
    }

    @Test
    fun gatewayUnreachableDoesNotStopOldReplicas() {
        val runtime = RollingFakeRuntime(autoReady = true)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v1)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v1)

        val store = RollingFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v2,
                    replicas = 2,
                    batchSize = 1,
                    serviceSlug = "demo",
                ),
            ),
        )
        val status = InMemoryReconcileStatusStore()
        val gateway = RecordingGatewayClient(unreachable = true)
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            maxActionsPerTick = 10,
            trafficShifter = TrafficShifter(gateway),
        )

        repeat(8) { controller.tickAll() }

        assertEquals(0, runtime.stopCalls.get())
        assertEquals(2, runtime.observe(deploymentId).replicas.count { it.image == v1 })
    }
}

private class RollingFakeDeploymentStore(
    private var desired: List<DesiredState>,
) : DeploymentStore {
    private val statuses = mutableMapOf<String, String>()

    override fun listDesired(): List<DesiredState> = desired
    override fun findDesired(deploymentId: UUID): DesiredState? =
        desired.find { it.deploymentId == deploymentId.toString() }

    override fun getStatus(deploymentId: UUID): String? =
        statuses[deploymentId.toString()]

    override fun setStatus(deploymentId: UUID, status: String) {
        statuses[deploymentId.toString()] = status
    }

    override fun setDesiredImage(deploymentId: UUID, image: String) {
        desired = desired.map {
            if (it.deploymentId == deploymentId.toString()) it.copy(image = image) else it
        }
    }
}

private class RecordingGatewayClient(
    private val unreachable: Boolean = false,
) : GatewayClient {
    val refreshCalls = AtomicInteger(0)

    override fun refreshRoutes(): ShiftResult {
        refreshCalls.incrementAndGet()
        if (unreachable) {
            return ShiftResult(ShiftOutcome.GatewayUnreachable, "down")
        }
        return ShiftResult(ShiftOutcome.Shifted, "ok")
    }
}

private class RollingFakeRuntime(
    private val autoReady: Boolean,
) : RuntimeClient {
    private data class W(var status: String, var image: String)
    private val workloads = ConcurrentHashMap<String, W>()
    val createCalls = AtomicInteger(0)
    val stopCalls = AtomicInteger(0)

    fun seed(runtimeId: String, status: String, image: String) {
        workloads[runtimeId] = W(status, image)
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
            .sortedBy { it.replicaIndex ?: Int.MAX_VALUE }
        return ActualState(replicas)
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        val w = workloads[runtimeDeploymentId] ?: return null
        return WorkloadHandle(runtimeDeploymentId, w.status, image = w.image)
    }

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            request.serviceSlug,
            request.deploymentId,
            request.replicaIndex,
        )
        val existing = workloads[runtimeId]
        if (existing != null) {
            val status = ReplicaStatus.parse(existing.status)
            val imageMatches = existing.image == request.image
            if (imageMatches && status in setOf(ReplicaStatus.Running, ReplicaStatus.Ready, ReplicaStatus.Pending)) {
                return EnsureOutcome.Adopted
            }
            workloads.remove(runtimeId)
            stopCalls.incrementAndGet()
        }
        val status = if (autoReady) "ready" else "running"
        workloads[runtimeId] = W(status, request.image)
        createCalls.incrementAndGet()
        return if (existing != null) EnsureOutcome.Recreated else EnsureOutcome.Created
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        workloads.remove(runtimeDeploymentId)
        stopCalls.incrementAndGet()
    }
}
