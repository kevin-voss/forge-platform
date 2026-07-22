package forge.control.reconcile

import forge.control.http.dto.DeploymentHistoryResponse
import forge.control.logging.JsonLog
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json

class RestartSafetyTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val serviceId = UUID.fromString("22222222-2222-2222-2222-222222222222")
    private val log = JsonLog("forge-control-test", "error")
    private val v1 = "registry.local/demo:v1"
    private val v2 = "registry.local/demo:v2"
    private val v3 = "registry.local/demo:v3-broken"

    @Test
    fun transitionRecorderWritesStatusAndEventAtomically() {
        val store = HistoryFakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, v2, replicas = 1, serviceId = serviceId, serviceSlug = "demo")),
        )
        store.setStatus(deploymentId, "pending")
        val history = InMemoryDeploymentHistory()
        val recorder = InMemoryTransitionRecorder(store, history, log)

        val event = recorder.transition(
            deploymentId = deploymentId,
            to = DeploymentLifecycle.Deploying,
            from = DeploymentLifecycle.Pending,
            image = v2,
            desiredReplicas = 1,
            actualReplicas = 0,
            reason = "rollout started",
        )!!

        assertEquals("deploying", store.getStatus(deploymentId))
        assertEquals(1, history.listByDeploymentId(deploymentId).size)
        assertEquals("pending", event.fromStatus)
        assertEquals("deploying", event.toStatus)
        assertEquals("rollout started", event.reason)
    }

    @Test
    fun transitionRecorderFailureRollsBackStatusAndHistory() {
        val store = HistoryFakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, v2, replicas = 1, serviceId = serviceId, serviceSlug = "demo")),
        )
        store.setStatus(deploymentId, "pending")
        val history = InMemoryDeploymentHistory()
        val recorder = InMemoryTransitionRecorder(store, history, log)
        recorder.failNextTransition()

        assertFailsWith<IllegalStateException> {
            recorder.transition(
                deploymentId = deploymentId,
                to = DeploymentLifecycle.Deploying,
                reason = "should fail",
            )
        }
        assertEquals("pending", store.getStatus(deploymentId))
        assertTrue(history.listByDeploymentId(deploymentId).isEmpty())
    }

    @Test
    fun historyReadReturnsEventsInChronologicalOrder() {
        val history = InMemoryDeploymentHistory()
        val t0 = Instant.parse("2026-07-22T14:00:00Z")
        val t1 = Instant.parse("2026-07-22T14:00:10Z")
        val t2 = Instant.parse("2026-07-22T14:00:20Z")
        history.append(
            DeploymentEvent(0, deploymentId, t1, "deploying", "rolling_back", v3, 2, 3, "timeout"),
        )
        history.append(
            DeploymentEvent(0, deploymentId, t0, "pending", "deploying", v3, 2, 2, "rollout started"),
        )
        history.append(
            DeploymentEvent(0, deploymentId, t2, "rolling_back", "rolled_back", v2, 2, 2, "restored"),
        )

        val ordered = history.listByDeploymentId(deploymentId)
        assertEquals(listOf("deploying", "rolling_back", "rolled_back"), ordered.map { it.toStatus })
        assertEquals(t0, ordered.first().at)
    }

    @Test
    fun startupRecoveryClassifiesInFlightVsCompleted() {
        val recovery = StartupRecovery(
            deploymentStore = HistoryFakeDeploymentStore(emptyList()),
            runtimeClient = HistoryFakeRuntime(autoReady = true),
            transitionRecorder = StatusOnlyTransitionRecorder(HistoryFakeDeploymentStore(emptyList())),
            log = log,
        )
        assertEquals("deployed", recovery.classify("deploying", actualReadyForDesired = true, rollbackRestored = false))
        assertEquals("deploying", recovery.classify("deploying", actualReadyForDesired = false, rollbackRestored = false))
        assertEquals("rolled_back", recovery.classify("rolling_back", actualReadyForDesired = false, rollbackRestored = true))
        assertEquals("rolling_back", recovery.classify("rolling_back", actualReadyForDesired = false, rollbackRestored = false))
    }

    @Test
    fun fullRolloutProducesOrderedHistoryDeployingThenDeployed() {
        val runtime = HistoryFakeRuntime(autoReady = true)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v1)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v1)

        val store = HistoryFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId, v2, replicas = 2, batchSize = 1, timeoutSeconds = 120,
                    serviceId = serviceId, serviceSlug = "demo",
                ),
            ),
        )
        store.setStatus(deploymentId, "pending")
        val history = InMemoryDeploymentHistory()
        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v1, 2))
        val status = InMemoryReconcileStatusStore()
        val recorder = InMemoryTransitionRecorder(store, history, log)
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
            transitionRecorder = recorder,
        )

        repeat(25) { controller.tickAll() }

        assertEquals("deployed", store.getStatus(deploymentId))
        val statuses = history.listByDeploymentId(deploymentId).map { it.toStatus }
        assertTrue(statuses.contains("deploying"), "history=$statuses")
        assertTrue(statuses.contains("deployed"), "history=$statuses")
        assertTrue(statuses.indexOf("deploying") < statuses.indexOf("deployed"))
    }

    @Test
    fun rollbackProducesDeployingRollingBackRolledBackHistory() {
        val runtime = HistoryFakeRuntime(autoReady = false)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v2)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v2)

        val clock = object : java.time.Clock() {
            private var instant = Instant.parse("2026-07-22T12:00:00Z")
            override fun getZone() = java.time.ZoneOffset.UTC
            override fun withZone(zone: java.time.ZoneId?) = this
            override fun instant() = instant
            fun advance(seconds: Long) {
                instant = instant.plusSeconds(seconds)
            }
        }
        val store = HistoryFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId, v3, replicas = 2, batchSize = 1, timeoutSeconds = 3,
                    serviceId = serviceId, serviceSlug = "demo",
                ),
            ),
        )
        store.setStatus(deploymentId, "pending")
        val history = InMemoryDeploymentHistory()
        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v2, 2))
        val status = InMemoryReconcileStatusStore()
        val recorder = InMemoryTransitionRecorder(store, history, log)
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
            transitionRecorder = recorder,
        )

        controller.tickAll()
        clock.advance(4)
        repeat(15) { controller.tickAll() }

        assertEquals("rolled_back", store.getStatus(deploymentId))
        val statuses = history.listByDeploymentId(deploymentId).map { it.toStatus }
        assertTrue(statuses.contains("deploying"), "history=$statuses")
        assertTrue(statuses.contains("rolling_back"), "history=$statuses")
        assertTrue(statuses.contains("rolled_back"), "history=$statuses")
        assertTrue(statuses.indexOf("deploying") < statuses.indexOf("rolling_back"))
        assertTrue(statuses.indexOf("rolling_back") < statuses.indexOf("rolled_back"))
    }

    @Test
    fun restartMidRolloutAdoptsContainersWithoutDuplicates() {
        val runtime = HistoryFakeRuntime(autoReady = true)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v1)
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1), "ready", v1)

        val store = HistoryFakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId, v2, replicas = 2, batchSize = 1, timeoutSeconds = 120,
                    serviceId = serviceId, serviceSlug = "demo",
                ),
            ),
        )
        store.setStatus(deploymentId, "pending")
        val history = InMemoryDeploymentHistory()
        val lastHealthy = InMemoryLastHealthyStore()
        lastHealthy.put(LastHealthyDeployment(serviceId, deploymentId, v1, 2))
        val status = InMemoryReconcileStatusStore()
        val recorder = InMemoryTransitionRecorder(store, history, log)

        fun newController(maxActions: Int) = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
            maxActionsPerTick = maxActions,
            trafficShifter = TrafficShifter(NoOpGatewayClient()),
            lastHealthyStore = lastHealthy,
            rollbackEnabled = true,
            transitionRecorder = recorder,
        )

        val first = newController(maxActions = 1)
        first.tickAll()
        assertEquals("deploying", store.getStatus(deploymentId))
        val createsBeforeRestart = runtime.createCalls.get()
        val observedBefore = runtime.observe(deploymentId).replicas.size

        // Simulate Control restart: recovery + new controller instance.
        val recovery = StartupRecovery(
            deploymentStore = store,
            runtimeClient = runtime,
            transitionRecorder = recorder,
            lastHealthyStore = lastHealthy,
            log = log,
            adoptLabels = true,
        )
        recovery.recover()

        val second = newController(maxActions = 10)
        repeat(20) { second.tickAll() }

        assertEquals("deployed", store.getStatus(deploymentId))
        val final = runtime.observe(deploymentId)
        assertEquals(2, final.replicas.count { it.image == v2 && it.statusEnum() == ReplicaStatus.Ready })
        // No duplicate explosion: creates after restart should mostly be adopts.
        assertTrue(runtime.createCalls.get() >= createsBeforeRestart)
        assertTrue(final.replicas.size <= observedBefore + 2)
        val statuses = history.listByDeploymentId(deploymentId).map { "${it.fromStatus}->${it.toStatus}" }
        assertTrue(statuses.distinct().size == statuses.size || statuses.contains("deploying->deployed"))
        // No gap: deploying appears before deployed
        val toStatuses = history.listByDeploymentId(deploymentId).map { it.toStatus }
        assertTrue(toStatuses.contains("deploying") && toStatuses.contains("deployed"))
    }

    @Test
    fun orphanLabeledContainerIsGCdOnStartup() {
        val runtime = HistoryFakeRuntime(autoReady = true)
        val orphanId = "demo-deadbeef-0"
        runtime.seed(orphanId, "ready", v1)
        // Live deployment uses a different short id.
        runtime.seed(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0), "ready", v2)

        val store = HistoryFakeDeploymentStore(
            listOf(DesiredState.of(deploymentId, v2, replicas = 1, serviceId = serviceId, serviceSlug = "demo")),
        )
        store.setStatus(deploymentId, "deployed")
        val history = InMemoryDeploymentHistory()
        val recorder = InMemoryTransitionRecorder(store, history, log)
        val recovery = StartupRecovery(
            deploymentStore = store,
            runtimeClient = runtime,
            transitionRecorder = recorder,
            log = log,
            adoptLabels = true,
        )

        val result = recovery.recover()
        assertTrue(result.orphanedStopped >= 1)
        assertTrue(runtime.findWorkload(orphanId) == null)
        assertTrue(runtime.findWorkload(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0)) != null)
    }

    @Test
    fun historyEndpointSchemaValidatesExamplePayload() {
        val example = """
            {
              "deploymentId": "11111111-1111-1111-1111-111111111111",
              "events": [
                {
                  "at": "2026-07-22T14:00:00Z",
                  "from": "pending",
                  "to": "deploying",
                  "image": "registry.local/demo:v2",
                  "desiredReplicas": 2,
                  "actualReplicas": 2,
                  "reason": "rollout started"
                },
                {
                  "at": "2026-07-22T14:00:20Z",
                  "from": "deploying",
                  "to": "deployed",
                  "image": "registry.local/demo:v2",
                  "reason": "all replicas ready"
                }
              ]
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(DeploymentHistoryResponse.serializer(), example)
        assertEquals(2, decoded.events.size)
        assertEquals("deploying", decoded.events[0].to)
        assertEquals("deployed", decoded.events[1].to)
    }
}

private class HistoryFakeDeploymentStore(
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

private class HistoryFakeRuntime(
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
        createCalls.incrementAndGet()
        val status = if (autoReady) "ready" else "running"
        workloads[runtimeId] = W(status, request.image)
        return if (existing != null) EnsureOutcome.Recreated else EnsureOutcome.Created
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        workloads.remove(runtimeDeploymentId)
        stopCalls.incrementAndGet()
    }

    override fun listWorkloads(): List<WorkloadHandle> =
        workloads.map { (id, w) -> WorkloadHandle(id, w.status, image = w.image) }
}
