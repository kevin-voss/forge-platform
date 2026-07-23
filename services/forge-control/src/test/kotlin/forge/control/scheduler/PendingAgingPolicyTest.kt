package forge.control.scheduler

import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

class PendingAgingPolicyTest {
    private val priorities = InMemoryPriorityClassStore().also {
        it.create("high", 100, PreemptionPolicy.PreemptLowerPriority)
        it.create("low", 0, PreemptionPolicy.Never)
    }

    @Test
    fun boostsExactlyOncePerThresholdCrossing() {
        val policy = PendingAgingPolicy(
            priorityClasses = priorities,
            starvationSeconds = 300,
            maxBoost = 10,
        )
        assertEquals(0, policy.ageBoost(299))
        assertEquals(1, policy.ageBoost(300))
        assertEquals(1, policy.ageBoost(599))
        assertEquals(2, policy.ageBoost(600))
        assertEquals(10, policy.ageBoost(100_000))
    }

    @Test
    fun orderForDrainPromotesAgedLowPriorityAheadOfFreshHigh() {
        val now = Instant.parse("2026-07-23T12:10:00Z")
        val policy = PendingAgingPolicy(
            priorityClasses = priorities,
            starvationSeconds = 300,
            clock = { now },
        )
        val lowAged = Placement(
            id = "plc_low",
            deploymentId = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
            replicaIndex = 0,
            nodeId = null,
            strategy = "pending",
            reason = "queued",
            createdAt = Instant.parse("2026-07-23T12:00:00Z"), // 600s old → boost 2
            status = PendingQueue.STATUS_PENDING,
            priorityClass = "low",
        )
        val highFresh = Placement(
            id = "plc_high",
            deploymentId = UUID.fromString("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
            replicaIndex = 0,
            nodeId = null,
            strategy = "pending",
            reason = "queued",
            createdAt = Instant.parse("2026-07-23T12:09:00Z"), // 60s old → boost 0
            status = PendingQueue.STATUS_PENDING,
            priorityClass = "high",
        )
        // low base 0 + boost 2 = 2; high base 100 + 0 = 100 → high still first
        val orderedHighWins = policy.orderForDrain(listOf(lowAged, highFresh), now)
        assertEquals(listOf("plc_high", "plc_low"), orderedHighWins.map { it.id })

        // Age low enough to surpass high: boost needs > 100 → use tiny starvation.
        val aggressive = PendingAgingPolicy(
            priorityClasses = priorities,
            starvationSeconds = 1,
            maxBoost = 200,
            clock = { now },
        )
        val ordered = aggressive.orderForDrain(listOf(lowAged, highFresh), now)
        assertEquals("plc_low", ordered.first().id)
    }
}
