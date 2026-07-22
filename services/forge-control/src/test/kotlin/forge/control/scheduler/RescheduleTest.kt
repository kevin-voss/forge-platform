package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.reconcile.ActualState
import forge.control.reconcile.DeploymentStore
import forge.control.reconcile.DesiredState
import forge.control.reconcile.EnsureOutcome
import forge.control.reconcile.InMemoryDeploymentHistory
import forge.control.reconcile.ReplicaObservation
import forge.control.reconcile.RuntimeClient
import forge.control.reconcile.WorkloadEnsureRequest
import forge.control.reconcile.WorkloadHandle
import forge.control.reconcile.WorkloadNamer
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class RescheduleTest {
    private val t0 = Instant.parse("2026-07-23T00:00:00Z")
    private val log = JsonLog("reschedule-test", "error")
    private val dpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

    @Test
    fun offlineMarksOnlyThatNodesPlacementsLostAndFreesCapacity() {
        val ctx = fixture(nodeASlots = 4, nodeBSlots = 4, desired = 4)
        placeSpread(ctx, replicas = 4)

        assertEquals(2, ctx.nodes.find("node-a")!!.allocation.slots)
        assertEquals(2, ctx.nodes.find("node-b")!!.allocation.slots)

        val count = ctx.handler.handleOfflineNow("node-b")
        assertEquals(2, count)

        val lost = ctx.placements.listByDeployment(dpl, PendingQueue.STATUS_LOST)
        assertEquals(2, lost.size)
        assertTrue(lost.all { it.nodeId == "node-b" })

        val activeOnB = ctx.placements.listByNode("node-b", PendingQueue.STATUS_PLACED)
        assertTrue(activeOnB.isEmpty())
        assertEquals(0, ctx.nodes.find("node-b")!!.allocation.slots)

        val placedOnA = ctx.placements.listByNode("node-a", PendingQueue.STATUS_PLACED)
        assertEquals(4, placedOnA.size)
        assertEquals(4, ctx.nodes.find("node-a")!!.allocation.slots)
    }

    @Test
    fun lostButDesiredGeneratesFreshPlacementRequest() {
        val ctx = fixture(nodeASlots = 2, nodeBSlots = 2, desired = 2)
        placeSpread(ctx, replicas = 2)
        val onB = ctx.placements.listByNode("node-b", PendingQueue.STATUS_PLACED)
        assertEquals(1, onB.size)
        val replica = onB.first().replicaIndex

        ctx.handler.handleOfflineNow("node-b")

        val replacement = ctx.placements.find(dpl, replica)
        assertEquals(PendingQueue.STATUS_PLACED, replacement!!.status)
        assertEquals("node-a", replacement.nodeId)
        assertEquals("node-b", replacement.rescheduledFromNode)

        val history = ctx.history.listByDeploymentId(dpl)
        assertTrue(history.any { it.reason?.startsWith("rescheduled:") == true })
    }

    @Test
    fun graceTimerSuppressesRescheduleForFastFlap() {
        val ctx = fixture(
            nodeASlots = 4,
            nodeBSlots = 4,
            desired = 4,
            grace = Duration.ofSeconds(30),
        )
        placeSpread(ctx, replicas = 4)
        ctx.handler.start()

        ctx.handler.onStatusTransition("node-b", "offline")
        ctx.handler.onStatusTransition("node-b", "online")
        Thread.sleep(100)

        val lost = ctx.placements.listByDeployment(dpl, PendingQueue.STATUS_LOST)
        assertTrue(lost.isEmpty(), "fast flap must not mark placements lost")
        assertEquals(2, ctx.placements.listByNode("node-b", PendingQueue.STATUS_PLACED).size)
        assertEquals(2, ctx.nodes.find("node-b")!!.allocation.slots)

        ctx.handler.stop()
    }

    @Test
    fun fencerStopsSurplusWhenRecoveredNodeExceedsDesired() {
        val placements = InMemoryPlacementStore()
        val runtime = FakeRuntime()
        placements.upsert(
            Placement(
                id = "plc0",
                deploymentId = dpl,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                serviceId = "svc",
            ),
        )
        placements.upsert(
            Placement(
                id = "plc1",
                deploymentId = dpl,
                replicaIndex = 1,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                serviceId = "svc",
            ),
        )
        // Lost row for index 2 (offline node); active replacement also for index 2.
        placements.upsert(
            Placement(
                id = "plc2-lost",
                deploymentId = dpl,
                replicaIndex = 2,
                nodeId = "node-b",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0,
                status = PendingQueue.STATUS_LOST,
                serviceId = "svc",
            ),
        )
        placements.upsert(
            Placement(
                id = "plc2-new",
                deploymentId = dpl,
                replicaIndex = 2,
                nodeId = "node-a",
                strategy = "least-allocated",
                reason = null,
                createdAt = t0.plusSeconds(1),
                serviceId = "svc",
                rescheduledFromNode = "node-b",
            ),
        )

        // Desired 2; actual still has 3 (stale recovered replica) → fence lost index first.
        val desired = DesiredState.of(dpl, image = "img:v1", replicas = 2, serviceSlug = "svc")
        val actual = ActualState(
            replicas = listOf(
                ReplicaObservation(replicaId = "0", status = "ready", replicaIndex = 0),
                ReplicaObservation(replicaId = "1", status = "ready", replicaIndex = 1),
                ReplicaObservation(replicaId = "2", status = "ready", replicaIndex = 2),
            ),
        )
        val fencer = StaleReplicaFencer(placements, runtime, log)
        val fenced = fencer.fence(desired, actual)
        assertEquals(listOf(2), fenced)
        assertTrue(runtime.stopped.isNotEmpty())
    }

    @Test
    fun noCapacityQueuesLostReplicasPendingAndDrainsWhenCapacityReturns() {
        val ctx = fixture(nodeASlots = 2, nodeBSlots = 2, desired = 4)
        placeSpread(ctx, replicas = 4)
        assertEquals(2, ctx.nodes.find("node-a")!!.allocation.slots)
        assertEquals(2, ctx.nodes.find("node-b")!!.allocation.slots)

        ctx.handler.handleOfflineNow("node-b")

        val pending = ctx.placements.listByDeployment(dpl, PendingQueue.STATUS_PENDING)
        assertEquals(2, pending.size)
        assertTrue(pending.all { it.rescheduledFromNode == "node-b" })

        // Free capacity on node-a by releasing two placed replicas, then drain.
        val onA = ctx.placements.listByNode("node-a", PendingQueue.STATUS_PLACED)
        ctx.service.releasePlacement(dpl, onA[0].replicaIndex)
        // release drains queue partially; free one more slot
        val stillPending = ctx.placements.countPending()
        if (stillPending > 0) {
            val remaining = ctx.placements.listByNode("node-a", PendingQueue.STATUS_PLACED)
            if (remaining.isNotEmpty()) {
                ctx.service.releasePlacement(dpl, remaining[0].replicaIndex)
            }
        }
        assertEquals(0, ctx.placements.countPending())
    }

    @Test
    fun recoverLostReplicasIsIdempotentAcrossRestart() {
        val ctx = fixture(nodeASlots = 4, nodeBSlots = 2, desired = 2)
        placeSpread(ctx, replicas = 2)
        val onB = ctx.placements.listByNode("node-b", PendingQueue.STATUS_PLACED).first()
        // Simulate crash mid-reschedule: node offline, placement lost, capacity freed, no replacement yet
        ctx.nodes.markOffline("node-b")
        ctx.placements.markLost(dpl, onB.replicaIndex)
        ctx.reservation.releaseSlots("node-b", 1)

        val first = ctx.handler.recoverLostReplicas()
        assertEquals(1, first)
        val active = ctx.placements.find(dpl, onB.replicaIndex)
        assertEquals(PendingQueue.STATUS_PLACED, active!!.status)
        assertEquals("node-a", active.nodeId)

        val second = ctx.handler.recoverLostReplicas()
        assertEquals(0, second)
    }

    private fun placeSpread(ctx: Fixture, replicas: Int) {
        repeat(replicas) { i ->
            val result = ctx.service.placeAndPersist(dpl, i, serviceId = "svc")
            assertIs<PlaceResult.Ok>(result)
        }
        val byNode = ctx.placements.listByDeployment(dpl, PendingQueue.STATUS_PLACED)
            .groupBy { it.nodeId }
        assertTrue(byNode.size >= 2 || replicas <= 1, "expected spread across nodes: $byNode")
    }

    private fun fixture(
        nodeASlots: Int,
        nodeBSlots: Int,
        desired: Int,
        grace: Duration = Duration.ZERO,
    ): Fixture {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = nodeASlots), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = nodeBSlots), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val sched = LeastAllocatedScheduler(nodes, reservation, AntiAffinityFilter(placements))
        val queue = PendingQueue(placements)
        val processor = QueueProcessor(
            queue = queue,
            scheduler = sched,
            store = placements,
            log = log,
            intervalMs = 60_000,
        )
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
            queueProcessor = processor,
        )
        val history = InMemoryDeploymentHistory()
        val deployments = RescheduleFakeDeploymentStore(desired)
        val handler = NodeOfflineHandler(
            store = placements,
            placementService = service,
            reservation = reservation,
            deploymentStore = deployments,
            log = log,
            enabled = true,
            grace = grace,
            history = history,
            nodeStore = nodes,
        )
        return Fixture(nodes, placements, reservation, service, handler, history)
    }

    private data class Fixture(
        val nodes: InMemoryNodeStore,
        val placements: InMemoryPlacementStore,
        val reservation: CapacityReservation,
        val service: PlacementService,
        val handler: NodeOfflineHandler,
        val history: InMemoryDeploymentHistory,
    )

    private class RescheduleFakeDeploymentStore(
        private val replicas: Int,
    ) : DeploymentStore {
        override fun listDesired(): List<DesiredState> =
            listOf(DesiredState.of(dpl, image = "img:v1", replicas = replicas, serviceSlug = "svc"))

        override fun findDesired(deploymentId: UUID): DesiredState? =
            if (deploymentId == dpl) {
                DesiredState.of(dpl, image = "img:v1", replicas = replicas, serviceSlug = "svc")
            } else {
                null
            }

        override fun getStatus(deploymentId: UUID): String? = "deployed"

        override fun setStatus(deploymentId: UUID, status: String) {}

        override fun setDesiredImage(deploymentId: UUID, image: String) {}

        companion object {
            private val dpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
        }
    }

    private class FakeRuntime : RuntimeClient {
        val stopped = CopyOnWriteArrayList<String>()
        private val workloads = ConcurrentHashMap<String, WorkloadHandle>()

        override fun loadActual(deploymentId: UUID): ActualState = ActualState()

        override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? =
            workloads[runtimeDeploymentId]

        override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
            val id = WorkloadNamer.runtimeDeploymentId(
                request.serviceSlug,
                request.deploymentId,
                request.replicaIndex,
            )
            workloads[id] = WorkloadHandle(id, "ready", image = request.image)
            return EnsureOutcome.Created
        }

        override fun stopWorkload(runtimeDeploymentId: String) {
            stopped += runtimeDeploymentId
            workloads.remove(runtimeDeploymentId)
        }
    }
}
