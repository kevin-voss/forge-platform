package forge.control.reconcile

import forge.control.logging.JsonLog
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID
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
    fun tickWritesStartPlanWithoutExecutingMutations() {
        val store = FakeDeploymentStore(
            listOf(
                DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 2),
            ),
        )
        val runtime = FakeRuntimeClient(
            mapOf(
                deploymentId to ActualState(listOf(ReplicaObservation("r1", "running"))),
            ),
        )
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
        assertEquals(1, snapshot.plan.size)
        assertEquals(ReconcileAction.StartReplica.name, snapshot.plan.actions[0].action)
        assertTrue(snapshot.controllerHealthy)
        assertEquals(0, runtime.startCalls.get())
        assertEquals(0, runtime.stopCalls.get())
        assertEquals(1, runtime.loadCalls.get())
    }

    @Test
    fun runtimeUnreachableSetsUnhealthyAndKeepsLastPlan() {
        val store = FakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 2)),
        )
        val runtime = FakeRuntimeClient(
            initial = mapOf(
                deploymentId to ActualState(listOf(ReplicaObservation("r1", "running"))),
            ),
        )
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
        assertEquals(1, firstPlan.size)

        runtime.unreachable = true
        controller.tickAll()

        val snapshot = status.findByDeploymentId(deploymentId)!!
        assertFalse(snapshot.controllerHealthy)
        assertEquals(firstPlan, snapshot.plan)
        assertEquals(0, runtime.startCalls.get())
        assertEquals(0, runtime.stopCalls.get())
    }

    @Test
    fun perDeploymentExceptionDoesNotBlockOthers() {
        val store = FakeDeploymentStore(
            listOf(
                DesiredState.of(deploymentId, "registry.local/demo:v1", replicas = 1),
                DesiredState.of(otherId, "registry.local/demo:v2", replicas = 1),
            ),
        )
        val runtime = object : RuntimeClient {
            override fun loadActual(id: UUID): ActualState {
                if (id == deploymentId) throw IllegalStateException("boom")
                return ActualState()
            }
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
        assertEquals(1, other.plan.size)
        assertEquals(ReconcileAction.StartReplica.name, other.plan.actions[0].action)
    }
}

private class FakeDeploymentStore(
    private val desired: List<DesiredState>,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> = desired
    override fun findDesired(deploymentId: UUID): DesiredState? =
        desired.find { it.deploymentId == deploymentId.toString() }
}

/**
 * Fake RuntimeClient. Intentionally has no start/stop execution API in 07.01;
 * counters prove the controller never "executes" by calling mutation hooks.
 */
private class FakeRuntimeClient(
    initial: Map<UUID, ActualState> = emptyMap(),
) : RuntimeClient {
    private val state = initial.toMutableMap()
    var unreachable: Boolean = false
    val loadCalls = AtomicInteger(0)
    val startCalls = AtomicInteger(0)
    val stopCalls = AtomicInteger(0)

    override fun loadActual(deploymentId: UUID): ActualState {
        loadCalls.incrementAndGet()
        if (unreachable) throw RuntimeUnreachableException("runtime down")
        return state[deploymentId] ?: ActualState()
    }
}
