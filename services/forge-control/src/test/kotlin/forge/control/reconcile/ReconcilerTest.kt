package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.scheduler.InMemoryPlacementStore
import forge.control.scheduler.PlacementService
import forge.control.scheduler.SingleNodeScheduler
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ReconcilerTest {
    private val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val log = JsonLog("forge-control-test", "error")

    @Test
    fun workloadNamerIsDeterministic() {
        val a = WorkloadNamer.runtimeDeploymentId("Demo API", deploymentId, 0)
        val b = WorkloadNamer.runtimeDeploymentId("Demo API", deploymentId, 0)
        assertEquals(a, b)
        assertEquals("demo-api-11111111-0", a)
        assertEquals("forge-demo-api-11111111-0", WorkloadNamer.containerName("Demo API", deploymentId, 0))
        assertEquals(
            WorkloadNamer.labels(deploymentId, "svc-1", 0, "img:v1")["forge.deployment"],
            deploymentId.toString(),
        )
    }

    @Test
    fun executeStartsTwoReplicasWithDistinctIndices() {
        val runtime = RecordingRuntimeClient()
        val reconciler = Reconciler(runtime, log, maxActionsPerTick = 5)
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 2,
            serviceSlug = "demo",
            port = 8080,
        )
        val plan = computePlan(desired, ActualState())
        assertEquals(2, plan.size)

        val results = reconciler.execute(desired, ActualState(), plan)

        assertEquals(2, results.size)
        assertEquals(setOf(0, 1), results.mapNotNull { it.replicaIndex }.toSet())
        assertEquals(2, runtime.createCalls.get())
        assertEquals(
            setOf("demo-11111111-0", "demo-11111111-1"),
            runtime.createdIds.toSet(),
        )
    }

    @Test
    fun startReplicaRecordsPlacementWithNodeId() {
        val runtime = RecordingRuntimeClient()
        val placementStore = InMemoryPlacementStore()
        val placementService = PlacementService(
            scheduler = SingleNodeScheduler("node-local"),
            store = placementStore,
            log = log,
        )
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = log,
            placementService = placementService,
        )
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
        )
        val plan = computePlan(desired, ActualState())

        val results = reconciler.execute(desired, ActualState(), plan)

        assertEquals(ActionResult.Created, results.single().result)
        val placement = placementStore.find(deploymentId, 0)
        assertEquals("node-local", placement?.nodeId)
        assertEquals("single-node", placement?.strategy)
    }

    @Test
    fun startReplicaFailsWhenNoNodeAvailable() {
        val runtime = RecordingRuntimeClient()
        val placementService = PlacementService(
            scheduler = SingleNodeScheduler(nodeId = null),
            store = InMemoryPlacementStore(),
            log = log,
        )
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = log,
            placementService = placementService,
        )
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
        )
        val results = reconciler.execute(desired, ActualState(), computePlan(desired, ActualState()))
        assertEquals(ActionResult.Failed, results.single().result)
        assertEquals("no_node_available", results.single().detail)
        assertEquals(0, runtime.createCalls.get())
    }

    @Test
    fun ensureWorkloadSkipsCreateWhenHealthyWorkloadExists() {
        val runtime = RecordingRuntimeClient()
        val runtimeId = WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0)
        runtime.seed(runtimeId, "running")
        val reconciler = Reconciler(runtime, log)
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
        )
        val plan = ReconcilePlan(
            listOf(
                ReconcileActionItem(
                    action = ReconcileAction.StartReplica.name,
                    reason = "desired=1 actual=0",
                    replicaId = "0",
                ),
            ),
        )

        val results = reconciler.execute(desired, ActualState(), plan)

        assertEquals(ActionResult.Adopted, results.single().result)
        assertEquals(0, runtime.createCalls.get())
    }

    @Test
    fun crashDetectorRecreatesFailedButNotHealthy() {
        assertTrue(CrashDetector.needsRecreate(ReplicaObservation("0", "failed", replicaIndex = 0)))
        assertTrue(!CrashDetector.needsRecreate(ReplicaObservation("0", "running", replicaIndex = 0)))

        val runtime = RecordingRuntimeClient()
        val runtimeId = WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 0)
        runtime.seed(runtimeId, "failed")
        val reconciler = Reconciler(runtime, log)
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
        )
        val actual = ActualState(
            listOf(ReplicaObservation("0", "failed", replicaIndex = 0)),
        )
        val plan = computePlan(desired, actual)
        assertEquals(1, plan.size)
        assertEquals("0", plan.actions[0].replicaId)

        val results = reconciler.execute(desired, actual, plan)
        assertEquals(ActionResult.Recreated, results.single().result)
        assertEquals(1, runtime.createCalls.get())
        assertEquals(1, runtime.stopCalls.get())
    }

    @Test
    fun actionsBoundedByMaxPerTick() {
        val runtime = RecordingRuntimeClient()
        val reconciler = Reconciler(runtime, log, maxActionsPerTick = 1)
        val desired = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 3,
            serviceSlug = "demo",
        )
        val plan = computePlan(desired, ActualState())
        assertEquals(3, plan.size)

        reconciler.execute(desired, ActualState(), plan)
        assertEquals(1, runtime.createCalls.get())
    }

    @Test
    fun integrationConvergesDesiredTwoAndIsIdempotent() {
        val runtime = RecordingRuntimeClient()
        val store = FakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    "registry.local/demo:v1",
                    replicas = 2,
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
            maxActionsPerTick = 5,
        )

        controller.tickAll()
        assertEquals(2, runtime.observe(deploymentId).replicas.size)
        assertEquals(2, runtime.createCalls.get())

        controller.tickAll()
        assertEquals(2, runtime.observe(deploymentId).replicas.size)
        assertEquals(2, runtime.createCalls.get()) // no new creates
        assertTrue(status.findByDeploymentId(deploymentId)!!.plan.actions.isEmpty())
    }

    @Test
    fun integrationRecreatesFailedReplicaExactlyOnce() {
        val runtime = RecordingRuntimeClient()
        val store = FakeDeploymentStore(
            listOf(
                DesiredState.of(
                    deploymentId,
                    "registry.local/demo:v1",
                    replicas = 2,
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
        )

        controller.tickAll()
        assertEquals(2, runtime.createCalls.get())

        val failedId = WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1)
        runtime.markStatus(failedId, "failed")
        val createsBefore = runtime.createCalls.get()
        controller.tickAll()
        assertEquals(createsBefore + 1, runtime.createCalls.get())
        assertEquals(2, runtime.observe(deploymentId).replicas.count { it.statusEnum() in setOf(ReplicaStatus.Running, ReplicaStatus.Ready, ReplicaStatus.Pending) })
    }

    @Test
    fun integrationStopsHighestIndexWhenDesiredDecreases() {
        val runtime = RecordingRuntimeClient()
        val desired2 = DesiredState.of(
            deploymentId,
            "registry.local/demo:v1",
            replicas = 2,
            serviceSlug = "demo",
        )
        val store = FakeDeploymentStore(listOf(desired2))
        val status = InMemoryReconcileStatusStore()
        val controller = ReconciliationController(
            deploymentStore = store,
            runtimeClient = runtime,
            statusStore = status,
            log = log,
            intervalMs = 2_000,
            enabled = true,
        )

        controller.tickAll()
        assertEquals(2, runtime.observe(deploymentId).replicas.size)

        store.replace(
            listOf(
                DesiredState.of(
                    deploymentId,
                    "registry.local/demo:v1",
                    replicas = 1,
                    serviceSlug = "demo",
                ),
            ),
        )
        controller.tickAll()

        val actual = runtime.observe(deploymentId)
        assertEquals(1, actual.replicas.size)
        assertEquals(0, actual.replicas.single().replicaIndex)
        assertEquals(1, runtime.stopCalls.get())
        assertTrue(
            runtime.stoppedIds.contains(WorkloadNamer.runtimeDeploymentId("demo", deploymentId, 1)),
        )
    }
}

private class FakeDeploymentStore(
    initial: List<DesiredState>,
) : DeploymentStore {
    private var desired: MutableList<DesiredState> = initial.toMutableList()
    private val statuses = mutableMapOf<String, String>()

    fun replace(next: List<DesiredState>) {
        desired = next.toMutableList()
    }

    override fun listDesired(): List<DesiredState> = desired.toList()
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
        }.toMutableList()
    }
}

/** In-memory Runtime that supports create/stop/observe for reconcile tests. */
private class RecordingRuntimeClient : RuntimeClient {
    private val workloads = ConcurrentHashMap<String, String>()
    val createCalls = AtomicInteger(0)
    val stopCalls = AtomicInteger(0)
    val createdIds = mutableListOf<String>()
    val stoppedIds = mutableListOf<String>()

    fun seed(runtimeId: String, status: String) {
        workloads[runtimeId] = status
    }

    fun markStatus(runtimeId: String, status: String) {
        require(workloads.containsKey(runtimeId))
        workloads[runtimeId] = status
    }

    override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

    override fun observe(deploymentId: UUID): ActualState {
        val replicas = workloads.entries
            .filter { WorkloadNamer.matchesDeployment(it.key, deploymentId) }
            .map { (id, status) ->
                val index = WorkloadNamer.parseReplicaIndex(id)
                ReplicaObservation(
                    replicaId = index?.toString() ?: id,
                    status = status,
                    replicaIndex = index,
                    workloadName = "forge-$id",
                )
            }
            .sortedBy { it.replicaIndex ?: Int.MAX_VALUE }
        return ActualState(replicas)
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        val status = workloads[runtimeDeploymentId] ?: return null
        return WorkloadHandle(runtimeDeploymentId, status)
    }

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            request.serviceSlug,
            request.deploymentId,
            request.replicaIndex,
        )
        val existing = workloads[runtimeId]
        if (existing != null) {
            val status = ReplicaStatus.parse(existing)
            if (status == ReplicaStatus.Running || status == ReplicaStatus.Ready || status == ReplicaStatus.Pending) {
                return EnsureOutcome.Adopted
            }
            workloads.remove(runtimeId)
            stopCalls.incrementAndGet()
            stoppedIds += runtimeId
            workloads[runtimeId] = "running"
            createCalls.incrementAndGet()
            createdIds += runtimeId
            return EnsureOutcome.Recreated
        }
        workloads[runtimeId] = "running"
        createCalls.incrementAndGet()
        createdIds += runtimeId
        return EnsureOutcome.Created
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        workloads.remove(runtimeDeploymentId)
        stopCalls.incrementAndGet()
        stoppedIds += runtimeDeploymentId
    }
}
