package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class PreemptionIntegrationTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")
    private val log = JsonLog("preemption-it", "error")

    @Test
    fun highPriorityPreemptsLowPriorityOnFullNode() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-d", "http://d", NodeCapacity(slots = 1), t0)
        val placements = InMemoryPlacementStore()
        val reservation = CapacityReservation(nodes)
        val priorities = InMemoryPriorityClassStore()
        priorities.create("high", 100, PreemptionPolicy.PreemptLowerPriority)
        priorities.create("low", -10, PreemptionPolicy.Never)
        val auditor = InMemoryPreemptionAuditor()
        val budgets = InMemoryDisruptionBudgetStore()
        val guard = DisruptionBudgetGuard(budgets, placements)
        val selector = PreemptionSelector(
            nodes = nodes,
            placements = placements,
            priorityClasses = priorities,
            budgetGuard = guard,
        )
        val scheduler = FirstFitScheduler(nodes, reservation)
        val pending = PendingQueue(placements)
        val evictor = GracefulEvictor(placements, reservation, log, grace = java.time.Duration.ZERO)
        val service = PlacementService(
            scheduler = scheduler,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = pending,
            priorityClasses = priorities,
            preemptionEnabled = true,
            preemptionSelector = selector,
            gracefulEvictor = evictor,
            preemptionAuditor = auditor,
        )
        evictor.resubmitFn = { lost -> service.resubmitFromLost(lost) }

        val lowDpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
        val highDpl = UUID.fromString("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
        val low = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(
                deploymentId = lowDpl,
                replicaIndex = 0,
                serviceId = "svc_low",
                priorityClass = "low",
                antiAffinity = AntiAffinity.Soft,
            ),
        )
        assertEquals("node-d", low.placement.nodeId)

        val high = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(
                deploymentId = highDpl,
                replicaIndex = 0,
                serviceId = "svc_high",
                priorityClass = "high",
            ),
        )
        assertEquals("node-d", high.placement.nodeId)
        assertEquals(1, auditor.list().size)
        assertEquals(low.placement.id, auditor.list().first().victimPlacementId)
        // Victim resubmitted as pending (no other capacity).
        val victimPending = placements.find(lowDpl, 0)
        assertEquals(PendingQueue.STATUS_PENDING, victimPending?.status)
    }

    @Test
    fun disruptionBudgetForcesPreemptorToPending() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-d", "http://d", NodeCapacity(slots = 1), t0)
        val placements = InMemoryPlacementStore()
        val reservation = CapacityReservation(nodes)
        val priorities = InMemoryPriorityClassStore()
        priorities.create("high", 100, PreemptionPolicy.PreemptLowerPriority)
        priorities.create("low", -10, PreemptionPolicy.Never)
        val budgets = InMemoryDisruptionBudgetStore()
        val guard = DisruptionBudgetGuard(budgets, placements)
        val selector = PreemptionSelector(
            nodes = nodes,
            placements = placements,
            priorityClasses = priorities,
            budgetGuard = guard,
        )
        val scheduler = FirstFitScheduler(nodes, reservation)
        val pending = PendingQueue(placements)
        val evictor = GracefulEvictor(placements, reservation, log, grace = java.time.Duration.ZERO)
        val service = PlacementService(
            scheduler = scheduler,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = pending,
            priorityClasses = priorities,
            preemptionSelector = selector,
            gracefulEvictor = evictor,
            preemptionAuditor = InMemoryPreemptionAuditor(),
        )
        evictor.resubmitFn = { lost -> service.resubmitFromLost(lost) }

        val lowDpl = UUID.fromString("cccccccc-cccc-cccc-cccc-cccccccccccc")
        val highDpl = UUID.fromString("dddddddd-dddd-dddd-dddd-dddddddddddd")
        assertIs<PlaceResult.Ok>(
            service.placeAndPersist(lowDpl, 0, priorityClass = "low"),
        )
        budgets.upsert(DisruptionBudget(deploymentId = lowDpl, minAvailable = 1, createdAt = t0))

        val result = service.placeAndPersist(highDpl, 0, priorityClass = "high")
        assertIs<PlaceResult.Pending>(result)
        // Low replica still placed.
        assertEquals(PendingQueue.STATUS_PLACED, placements.find(lowDpl, 0)?.status)
        assertTrue(placements.find(highDpl, 0)?.status == PendingQueue.STATUS_PENDING)
    }
}
