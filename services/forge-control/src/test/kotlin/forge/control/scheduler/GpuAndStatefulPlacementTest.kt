package forge.control.scheduler

import forge.control.scheduler.model.GpuCapacity
import forge.control.scheduler.model.GpuRequest
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.ResourceRequirements
import forge.control.scheduler.model.StatefulSpec
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class GpuAndStatefulPlacementTest {
    private val t0 = Instant.parse("2026-07-24T00:00:00Z")

    private fun gpuFleet(): Triple<InMemoryNodeStore, CapacityReservation, StatefulPlacementFilter> {
        val nodes = InMemoryNodeStore(
            OvercommitConfig(systemReservedCpuMillis = 0, systemReservedMemoryMb = 0),
        )
        nodes.register(
            "cpu-only",
            "http://cpu",
            NodeCapacity(slots = 4),
            t0,
        )
        nodes.register(
            "gpu-a100",
            "http://gpu",
            NodeCapacity(
                slots = 4,
                gpu = GpuCapacity(count = 2, vendor = "nvidia", model = "A100", memoryMb = 40 * 1024),
            ),
            t0,
        )
        val volumes = InMemoryVolumeLocalityStore()
        val filter = StatefulPlacementFilter(volumeLocality = volumes)
        return Triple(nodes, CapacityReservation(nodes), filter)
    }

    @Test
    fun gpuWorkloadSchedulesOnlyOntoMatchingGpuNode() {
        val (nodes, reservation, filter) = gpuFleet()
        val scheduler = FirstFitScheduler(nodes, reservation, statefulFilter = filter)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-gpu",
                replicaIndex = 0,
                requirements = ResourceRequirements(
                    slots = 1,
                    gpu = GpuRequest(count = 1, vendor = "nvidia", model = "A100"),
                ),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("gpu-a100", assigned.nodeId)
        assertTrue(assigned.trace!!.filters.any { it.name == "capacity" })
    }

    @Test
    fun noMatchingGpuYieldsInsufficientGpu() {
        val (nodes, reservation, filter) = gpuFleet()
        val scheduler = FirstFitScheduler(nodes, reservation, statefulFilter = filter)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-gpu",
                replicaIndex = 0,
                requirements = ResourceRequirements(
                    slots = 1,
                    gpu = GpuRequest(count = 1, vendor = "nvidia", model = "H100"),
                ),
            ),
        )
        val none = assertIs<PlacementDecision.NoNodeAvailable>(decision)
        assertTrue(
            none.unschedulableReasons.any { it.reason == "InsufficientGpu" },
            "expected InsufficientGpu, got ${none.unschedulableReasons}",
        )
        assertTrue(none.reason.contains("InsufficientGpu") || none.reason.contains("gpu"))
    }

    @Test
    fun reservationHoldsCapacityUntilExpiry() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        val capacity = CapacityReservation(nodes)
        val store = InMemoryReservationStore()
        var now = t0
        val service = ReservationService(
            store = store,
            nodes = nodes,
            capacityReservation = capacity,
            clock = { now },
        )
        val hold = service.create(
            name = "warm-pool-1",
            resources = ReservationResources(slots = 1),
            expiresAfter = Duration.ofMinutes(30),
        )
        assertEquals("node-a", hold.nodeId)
        assertEquals(0, PlacementCapacity.freeSlots(nodes.find("node-a")!!))

        // Competing placement cannot use held capacity.
        val scheduler = FirstFitScheduler(nodes, capacity)
        val blocked = scheduler.place(
            PlacementRequest(deploymentId = "other", replicaIndex = 0),
        )
        assertIs<PlacementDecision.NoNodeAvailable>(blocked)

        now = t0.plus(Duration.ofMinutes(31))
        assertEquals(1, service.releaseExpired(now))
        assertEquals(CapacityHold.STATUS_EXPIRED, store.find("warm-pool-1")!!.status)
        assertEquals(1, PlacementCapacity.freeSlots(nodes.find("node-a")!!))
    }

    @Test
    fun statefulVolumeLocalityIsPreserved() {
        val (nodes, reservation, _) = gpuFleet()
        val volumes = InMemoryVolumeLocalityStore()
        volumes.put("invoice-db-data", "cpu-only")
        val filter = StatefulPlacementFilter(volumeLocality = volumes)
        val scheduler = FirstFitScheduler(nodes, reservation, statefulFilter = filter)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-db",
                replicaIndex = 0,
                serviceId = "svc-db",
                placement = PlacementSpec(
                    stateful = StatefulSpec(
                        volumeRef = "invoice-db-data",
                        role = "primary",
                        migrationPolicy = "manual-approval",
                    ),
                ),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("cpu-only", assigned.nodeId)
        assertEquals("cpu-only", volumes.get("invoice-db-data"))
    }

    @Test
    fun protectedPrimaryIsNotPreempted() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 1), t0)
        val reservation = CapacityReservation(nodes)
        reservation.tryReserve("node-a", ResourceRequirements(slots = 1))
        val placements = InMemoryPlacementStore()
        val dep = UUID.fromString("11111111-1111-1111-1111-111111111111")
        placements.upsert(
            Placement(
                id = "plc_primary",
                deploymentId = dep,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                slots = 1,
                priorityClass = "low",
                stateful = StatefulSpec(
                    role = "primary",
                    migrationPolicy = "manual-approval",
                    volumeRef = "vol-1",
                ),
            ),
        )
        val priorities = InMemoryPriorityClassStore().also {
            it.ensureDefault()
            it.create("high", 100, PreemptionPolicy.PreemptLowerPriority)
            it.create("low", -10, PreemptionPolicy.Never)
        }
        val guard = StatefulPrimaryGuard()
        val selector = PreemptionSelector(
            nodes = nodes,
            placements = placements,
            priorityClasses = priorities,
            statefulGuard = guard,
        )
        val selection = selector.findMinimalVictims(
            PlacementRequest(
                deploymentId = "dpl-high",
                replicaIndex = 0,
                priorityClass = "high",
            ),
            priorities.resolve("high"),
        )
        assertEquals(null, selection, "protected primary must not be a preemption victim")
        assertFalse(guard.allowsVoluntaryRemoval(placements.findById("plc_primary")!!).allowed)

        guard.approveMigration(dep, 0)
        assertTrue(guard.allowsVoluntaryRemoval(placements.findById("plc_primary")!!).allowed)
    }

    @Test
    fun primaryAntiAffinityBlocksSecondPrimaryOnSameNode() {
        val nodes = InMemoryNodeStore()
        nodes.register("node-a", "http://a", NodeCapacity(slots = 2), t0)
        nodes.register("node-b", "http://b", NodeCapacity(slots = 2), t0)
        val reservation = CapacityReservation(nodes)
        val placements = InMemoryPlacementStore()
        val dep = UUID.fromString("22222222-2222-2222-2222-222222222222")
        placements.upsert(
            Placement(
                id = "plc_p0",
                deploymentId = dep,
                replicaIndex = 0,
                nodeId = "node-a",
                strategy = "first-fit",
                reason = "seed",
                createdAt = t0,
                serviceId = "svc-db",
                slots = 1,
                stateful = StatefulSpec(role = "primary", volumeRef = "vol-db"),
            ),
        )
        reservation.tryReserve("node-a", ResourceRequirements(slots = 1))
        val filter = StatefulPlacementFilter(
            volumeLocality = InMemoryVolumeLocalityStore(),
            placedReplicas = { placements.listPlaced() },
        )
        val scheduler = FirstFitScheduler(nodes, reservation, statefulFilter = filter)
        val decision = scheduler.place(
            PlacementRequest(
                deploymentId = "dpl-p1",
                replicaIndex = 0,
                serviceId = "svc-db",
                placement = PlacementSpec(
                    stateful = StatefulSpec(role = "primary", volumeRef = "vol-db"),
                ),
            ),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-b", assigned.nodeId)
    }

    @Test
    fun nodeAllocatableIncludesGpu() {
        val capacity = NodeCapacity(
            slots = 2,
            cpuMillis = 2000,
            gpu = GpuCapacity(count = 1, vendor = "nvidia", model = "A100"),
        )
        val alloc = CapacityAccounting.allocatable(capacity)
        assertNotNull(alloc.gpu)
        assertEquals(1, alloc.gpu!!.count)
        assertEquals("A100", alloc.gpu!!.model)
    }
}
