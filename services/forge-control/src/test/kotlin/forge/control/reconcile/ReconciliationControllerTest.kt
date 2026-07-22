package forge.control.reconcile

import forge.control.logging.JsonLog
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class ReconciliationControllerTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val otherId = UUID.fromString("22222222-2222-2222-2222-222222222222")
    private val clock = Clock.fixed(Instant.parse("2026-07-22T14:00:00Z"), ZoneOffset.UTC)
    private val log = JsonLog("forge-control-test", "error")

    @Test
    fun tickExecutesStartPlanAndConverges() {
        val store = ControllerFakeDeploymentStore(
            listOf(
                DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 2, serviceSlug = "demo"),
            ),
        )
        val runtime = ControllerFakeRuntimeClient()
        val status = InMemoryReconcileStatusStore()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            clock = clock,
        )

        controller.tickAll()

        val snapshot = status.findByDeploymentId(deploymentId)!!
        assertTrue(snapshot.plan.actions.isEmpty())
        assertTrue(snapshot.controllerHealthy)
        assertEquals(2, snapshot.actual.replicas.size)
        assertEquals(2, runtime.createCalls.get())
        assertEquals(0, runtime.stopCalls.get())
    }

    @Test
    fun runtimeUnreachableSetsUnhealthyAndKeepsLastPlan() {
        val store = ControllerFakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 2, serviceSlug = "demo")),
        )
        val runtime = ControllerFakeRuntimeClient()
        val status = InMemoryReconcileStatusStore()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            clock = clock,
        )

        controller.tickAll()
        val firstPlan = status.findByDeploymentId(deploymentId)!!.plan
        assertTrue(firstPlan.actions.isEmpty())

        runtime.unreachable = true
        controller.tickAll()

        val snapshot = status.findByDeploymentId(deploymentId)!!
        assertFalse(snapshot.controllerHealthy)
        assertEquals(firstPlan, snapshot.plan)
    }

    @Test
    fun perDeploymentExceptionDoesNotBlockOthers() {
        val store = ControllerFakeDeploymentStore(
            listOf(
                DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 1, serviceSlug = "demo"),
                DesiredState.of(otherId, "registry.local/demo:v2", replicas = 1, serviceSlug = "other"),
            ),
        )
        val runtime = object : RuntimeClient {
            override fun loadActual(id: UUID): ActualState {
                if (id == deploymentId) throw IllegalStateException("boom")
                return ActualState()
            }

            override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? = null

            override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
                if (request.deploymentId == deploymentId) throw IllegalStateException("boom")
                return EnsureOutcome.Created
            }

            override fun stopWorkload(runtimeDeploymentId: String) = Unit
        }
        val status = InMemoryReconcileStatusStore()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            clock = clock,
        )

        controller.tickAll()

        assertEquals(null, status.findByDeploymentId(deploymentId))
        val other = status.findByDeploymentId(otherId)!!
        assertTrue(other.controllerHealthy)
    }

    @Test
    fun runtimeCreateFailureDoesNotCrashController() {
        val store = ControllerFakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 1, serviceSlug = "demo")),
        )
        val runtime = ControllerFakeRuntimeClient(failCreates = true)
        val status = InMemoryReconcileStatusStore()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            clock = clock,
        )

        controller.tickAll()

        val snapshot = status.findByDeploymentId(deploymentId)!!
        assertTrue(snapshot.controllerHealthy)
        assertEquals(1, snapshot.plan.size)
        assertEquals(ReconcileAction.StartReplica.name, snapshot.plan.actions[0].action)
    }
}

private class ControllerFakeDeploymentStore(
    private val desired: List<DesiredState>,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> = desired
    override fun findDesired(deploymentId: UUID): DesiredState? =
        desired.find { it.deploymentId == deploymentId.toString() }
}

private class ControllerFakeRuntimeClient(
    private val failCreates: Boolean = false,
) : RuntimeClient {
    private val workloads = ConcurrentHashMap<String, String>()
    var unreachable: Boolean = false
    val createCalls = AtomicInteger(0)
    val stopCalls = AtomicInteger(0)

    override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

    override fun observe(deploymentId: UUID): ActualState {
        if (unreachable) throw RuntimeUnreachableException("runtime down")
        val replicas = workloads.entries
            .filter { WorkloadNamer.matchesDeployment(it.key, deploymentId) }
            .map { (id, status) ->
                val index = WorkloadNamer.parseReplicaIndex(id)
                ReplicaObservation(
                    replicaId = index?.toString() ?: id,
                    status = status,
                    replicaIndex = index,
                )
            }
        return ActualState(replicas)
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        if (unreachable) throw RuntimeUnreachableException("runtime down")
        val status = workloads[runtimeDeploymentId] ?: return null
        return WorkloadHandle(runtimeDeploymentId, status)
    }

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
        if (unreachable) throw RuntimeUnreachableException("runtime down")
        if (failCreates) throw RuntimeApiException("create failed")
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            request.serviceSlug,
            request.deploymentId,
            request.replicaIndex,
        )
        if (workloads.containsKey(runtimeId)) return EnsureOutcome.Adopted
        workloads[runtimeId] = "running"
        createCalls.incrementAndGet()
        return EnsureOutcome.Created
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        if (unreachable) throw RuntimeUnreachableException("runtime down")
        workloads.remove(runtimeDeploymentId)
        stopCalls.incrementAndGet()
    }
}
