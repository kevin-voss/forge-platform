package forge.control.reconcile

import forge.control.logging.JsonLog
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RollbackTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val serviceId = UUID.fromString("22222222-2222-2222-2222-222222222222")
    private val log = JsonLog("forge-control-test", "error")
    private val v2 = "registry.local/demo:v2"
    private val v3 = "registry.local/demo:v3-broken"

    @Test
    fun healthEvaluatorSuccessBeforeTimeout() {
        val desired = DesiredState.of(deploymentId, v2, replicas = 2, serviceId = serviceId, serviceSlug = "demo")
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "ready", replicaIndex = 0, image = v2),
                ReplicaObservation("1", "ready", replicaIndex = 1, image = v2),
            ),
        )
        assertEquals(RolloutHealth.Success, HealthEvaluator().evaluate(desired, actual, timedOut = true))
    }

    @Test
    fun healthEvaluatorTimeoutWithNotReadyIsFailure() {
        val desired = DesiredState.of(deploymentId, v3, replicas = 2, serviceId = serviceId, serviceSlug = "demo")
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "ready", replicaIndex = 0, image = v2),
                ReplicaObservation("1", "ready", replicaIndex = 1, image = v2),
                ReplicaObservation("2", "running", replicaIndex = 2, image = v3),
            ),
        )
        assertEquals(RolloutHealth.Failed, HealthEvaluator().evaluate(desired, actual, timedOut = true))
        assertEquals(RolloutHealth.InProgress, HealthEvaluator().evaluate(desired, actual, timedOut = false))
    }

    @Test
    fun healthEvaluatorFailedTargetIsImmediateFailure() {
        val desired = DesiredState.of(deploymentId, v3, replicas = 2, serviceId = serviceId, serviceSlug = "demo")
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "ready", replicaIndex = 0, image = v2),
                ReplicaObservation("2", "failed", replicaIndex = 2, image = v3),
            ),
        )
        assertEquals(RolloutHealth.Failed, HealthEvaluator().evaluate(desired, actual, timedOut = false))
    }

    @Test
    fun rolloutTimerUsesInjectableClock() {
        val start = Instant.parse("2026-07-22T12:00:00Z")
        val clock = MutableClock(start)
        val timer = RolloutTimer(clock)
        timer.start(deploymentId.toString())
        assertFalse(timer.isTimedOut(deploymentId.toString(), timeoutSeconds = 5))
        clock.advance(Duration.ofSeconds(5))
        assertTrue(timer.isTimedOut(deploymentId.toString(), timeoutSeconds = 5))
    }

    @Test
    fun rollbackerProducesStopTargetAndRestoreOldActions() {
        val desired = DesiredState.of(deploymentId, v3, replicas = 2, serviceId = serviceId, serviceSlug = "demo")
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "ready", replicaIndex = 0, image = v2),
                ReplicaObservation("1", "ready", replicaIndex = 1, image = v2),
                ReplicaObservation("2", "running", replicaIndex = 2, image = v3),
            ),
        )
        val lastHealthy = LastHealthyDeployment(serviceId, deploymentId, v2, 2)
        val plan = Rollbacker().planRollback(desired, actual, lastHealthy, failedTargetImage = v3)
        assertTrue(plan.actions.any { it.action == ReconcileAction.StopReplica.name && it.replicaId == "2" })
        assertTrue(plan.actions.any { it.action == ReconcileAction.ShiftTraffic.name })
        assertFalse(plan.actions.any { it.action == ReconcileAction.StopReplica.name && it.replicaId == "0" })
    }

    @Test
    fun rollbackerIdempotentWhenPartiallyApplied() {
        val desired = DesiredState.of(deploymentId, v2, replicas = 2, serviceId = serviceId, serviceSlug = "demo")
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "ready", replicaIndex = 0, image = v2),
                ReplicaObservation("1", "ready", replicaIndex = 1, image = v2),
            ),
        )
        val lastHealthy = LastHealthyDeployment(serviceId, deploymentId, v2, 2)
        val plan = Rollbacker().planRollback(desired, actual, lastHealthy, failedTargetImage = v3)
        assertFalse(plan.actions.any { it.action == ReconcileAction.StopReplica.name })
        assertFalse(plan.actions.any { it.action == ReconcileAction.StartReplica.name })
        assertTrue(Rollbacker().isRestored(actual, lastHealthy))
    }

    @Test
    fun unhealthyRolloutTimesOutAndRollsBackToLastHealthy() {
        val clock = MutableClock(Instant.parse("2026-07-22T12:00:00Z"))
        val runtime = RollbackFakeRuntime(autoReady = false)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v2)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v2)

        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v2, 2))
        val store = RollbackFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v3,
                    replicas = 2,
                    batchSize = 1,
                    timeoutSeconds = 3,
                    serviceId = serviceId,
                    serviceSlug = "demo",
                ),
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
            maxActionsPerTick = 10,
            clock = clock,
            trafficShifter = TrafficShifter(NoOpGatewayClient()),
            readinessMaxWaitSeconds = 1,
            lastHealthyStore = lastHealthy,
            rolloutTimer = RolloutTimer(clock),
            rollbackEnabled = true,
        )

        // Start rollout (surge v3, hold on readiness)
        controller.tickAll()
        assertEquals("deploying", status.findByDeploymentId(deploymentId)!!.deploymentStatus)

        // Advance past timeout and finish rollback
        clock.advance(Duration.ofSeconds(4))
        repeat(12) { controller.tickAll() }

        val snap = status.findByDeploymentId(deploymentId)!!
        assertEquals("rolled_back", snap.deploymentStatus)
        assertEquals(v2, snap.lastHealthyImage)
        val final = runtime.observe(deploymentId)
        assertEquals(2, final.replicas.count { it.image == v2 && it.statusEnum() == ReplicaStatus.Ready })
        assertEquals(0, final.replicas.count { it.image == v3 })
        assertEquals(v2, store.findDesired(deploymentId)!!.image)
    }

    @Test
    fun successfulRolloutMarksDeployedAndUpdatesLastHealthy() {
        val runtime = RollbackFakeRuntime(autoReady = true)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v2)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v2)

        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v2, 2))
        val v2b = "registry.local/demo:v2-next"
        val store = RollbackFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v2b,
                    replicas = 2,
                    batchSize = 1,
                    timeoutSeconds = 120,
                    serviceId = serviceId,
                    serviceSlug = "demo",
                ),
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
            maxActionsPerTick = 10,
            trafficShifter = TrafficShifter(NoOpGatewayClient()),
            lastHealthyStore = lastHealthy,
            rollbackEnabled = true,
        )

        repeat(20) {
            controller.tickAll()
            val actual = runtime.observe(deploymentId)
            if (actual.replicas.size == 2 && actual.replicas.all { it.image == v2b }) return@repeat
        }

        val snap = status.findByDeploymentId(deploymentId)!!
        assertEquals("deployed", snap.deploymentStatus)
        assertEquals(v2b, lastHealthy.get(serviceId)!!.image)
        assertEquals(v2b, snap.lastHealthyImage)
    }

    @Test
    fun controllerRestartMidRollbackCompletesWithoutDuplicateStops() {
        val clock = MutableClock(Instant.parse("2026-07-22T12:00:00Z"))
        val runtime = RollbackFakeRuntime(autoReady = false)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v2)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v2)

        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v2, 2))
        val store = RollbackFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    v3,
                    replicas = 2,
                    batchSize = 1,
                    timeoutSeconds = 2,
                    serviceId = serviceId,
                    serviceSlug = "demo",
                ),
            ),
        )
        val status = InMemoryReconcileStatusStore()
        val timer = RolloutTimer(clock)

        fun newController(maxActions: Int = 1) = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            maxActionsPerTick = maxActions,
            clock = clock,
            trafficShifter = TrafficShifter(NoOpGatewayClient()),
            readinessMaxWaitSeconds = 1,
            lastHealthyStore = lastHealthy,
            rolloutTimer = timer,
            rollbackEnabled = true,
        )

        val first = newController(maxActions = 1)
        first.tickAll()
        clock.advance(Duration.ofSeconds(3))
        first.tickAll() // enter rolling_back; maxActions=1 leaves work unfinished
        assertEquals("rolling_back", status.findByDeploymentId(deploymentId)!!.deploymentStatus)
        assertTrue(runtime.observe(deploymentId).replicas.any { it.image == v3 })
        val stopsAfterFirst = runtime.stopCalls.get()

        // "Restart" — new controller instance, same persisted status/lastHealthy
        val second = newController(maxActions = 10)
        repeat(10) { second.tickAll() }

        assertEquals("rolled_back", status.findByDeploymentId(deploymentId)!!.deploymentStatus)
        assertEquals(0, runtime.observe(deploymentId).replicas.count { it.image == v3 })
        assertTrue(runtime.stopCalls.get() >= stopsAfterFirst)
        // Idempotent: no runaway stop storm after restore
        val afterRestoreStops = runtime.stopCalls.get()
        second.tickAll()
        second.tickAll()
        assertEquals(afterRestoreStops, runtime.stopCalls.get())
    }
}

private class MutableClock(private var instant: Instant) : Clock() {
    override fun getZone() = ZoneOffset.UTC
    override fun withZone(zone: java.time.ZoneId?) = this
    override fun instant() = instant
    fun advance(duration: Duration) {
        instant = instant.plus(duration)
    }
}

private class RollbackFakeDeploymentStore(
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

private class RollbackFakeRuntime(
    private val autoReady: Boolean,
) : RuntimeClient {
    private data class W(var status: String, var image: String)
    private val workloads = ConcurrentHashMap<String, W>()
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
        return if (existing != null) EnsureOutcome.Recreated else EnsureOutcome.Created
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        workloads.remove(runtimeDeploymentId)
        stopCalls.incrementAndGet()
    }
}
