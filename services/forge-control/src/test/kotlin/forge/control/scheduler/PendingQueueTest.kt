package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

class PendingQueueTest {
    private val t0 = Instant.parse("2026-07-22T12:00:00Z")
    private val log = JsonLog("pending-queue-test", "error")

    @Test
    fun fifoRetryPlacesWhenCapacityFrees() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val antiAffinity = AntiAffinityFilter(placements)
        val sched = FirstFitScheduler(nodes, reservation, antiAffinity)
        val queue = PendingQueue(placements, maxLen = 10)
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
        val dpl = UUID.fromString("44444444-4444-4444-4444-444444444444")

        val first = assertIs<PlaceResult.Ok>(service.placeAndPersist(dpl, 0, serviceId = "svc"))
        assertEquals("node-a", first.placement.nodeId)

        val pending = assertIs<PlaceResult.Pending>(service.placeAndPersist(dpl, 1, serviceId = "svc"))
        assertEquals("pending", pending.placement.status)
        assertEquals(1, queue.count())
        assertEquals(0, processor.processOnce())

        service.releasePlacement(dpl, 0)
        // releasePlacement drains the queue: pending replica-1 is placed immediately.
        val placed = placements.find(dpl, 1)
        assertEquals("placed", placed?.status)
        assertEquals("node-a", placed?.nodeId)
        assertEquals(1, nodes.find("node-a")!!.allocation.slots)
        assertEquals(0, queue.count())
    }

    @Test
    fun exceedingCapacityLeavesPendingWithoutOverCommit() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val sched = LeastAllocatedScheduler(nodes, reservation, AntiAffinityFilter(placements))
        val queue = PendingQueue(placements)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
        )
        val dpl = UUID.fromString("55555555-5555-5555-5555-555555555555")
        val results = (0 until 6).map { i ->
            service.placeAndPersist(dpl, i, serviceId = "svc_spread", antiAffinity = AntiAffinity.Soft)
        }
        val placed = results.filterIsInstance<PlaceResult.Ok>()
        val pending = results.filterIsInstance<PlaceResult.Pending>()
        assertEquals(4, placed.size)
        assertEquals(2, pending.size)
        assertEquals(2, nodes.find("node-a")!!.allocation.slots)
        assertEquals(2, nodes.find("node-b")!!.allocation.slots)
        assertEquals(2, queue.count())
    }

    @Test
    fun softSpreadPlacesOnePerNodeAcrossTwoNodes() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val sched = LeastAllocatedScheduler(nodes, reservation, AntiAffinityFilter(placements))
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = PendingQueue(placements),
        )
        val dpl = UUID.fromString("66666666-6666-6666-6666-666666666666")
        val a = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(dpl, 0, serviceId = "svc", antiAffinity = AntiAffinity.Soft),
        )
        val b = assertIs<PlaceResult.Ok>(
            service.placeAndPersist(dpl, 1, serviceId = "svc", antiAffinity = AntiAffinity.Soft),
        )
        assertTrue(a.placement.nodeId != b.placement.nodeId)
    }

    @Test
    fun queueFullRejectsWithoutSilentDrop() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val sched = FirstFitScheduler(nodes, reservation)
        val queue = PendingQueue(placements, maxLen = 1)
        val service = PlacementService(
            scheduler = sched,
            store = placements,
            log = log,
            reservation = reservation,
            pendingQueue = queue,
        )
        val dpl = UUID.fromString("77777777-7777-7777-7777-777777777777")
        assertIs<PlaceResult.Ok>(service.placeAndPersist(dpl, 0))
        assertIs<PlaceResult.Pending>(service.placeAndPersist(dpl, 1))
        assertIs<PlaceResult.QueueFull>(service.placeAndPersist(dpl, 2))
        assertFailsWith<QueueFullException> {
            queue.enqueue(dpl, 3, reason = "full")
        }
    }

    @Test
    fun newNodeRegistrationDrainsPending() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val sched = FirstFitScheduler(nodes, reservation, AntiAffinityFilter(placements))
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
        val dpl = UUID.fromString("88888888-8888-8888-8888-888888888888")
        assertIs<PlaceResult.Ok>(service.placeAndPersist(dpl, 0))
        assertIs<PlaceResult.Pending>(service.placeAndPersist(dpl, 1))

        nodes.register("node-b", "http://b", NodeCapacity(slots = 1), t0)
        assertEquals(1, service.drainQueue())
        assertEquals("placed", placements.find(dpl, 1)?.status)
        assertEquals("node-b", placements.find(dpl, 1)?.nodeId)
    }
}
