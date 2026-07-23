package forge.control.resource

import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals

class ConditionMergeTest {
    private val t0 = Instant.parse("2026-07-23T10:00:00Z")
    private val t1 = Instant.parse("2026-07-23T10:05:00Z")

    @Test
    fun sameStatusMessageOnlyUpdateKeepsLastTransitionTime() {
        val existing = Condition(
            type = "Available",
            status = "True",
            reason = "OK",
            message = "old",
            lastTransitionTime = t0.toString(),
        )
        val next = Condition(
            type = "Available",
            status = "True",
            reason = "StillOK",
            message = "new message",
        )
        val merged = ConditionMerge.mergeCondition(existing, next, now = t1)
        assertEquals(t0.toString(), merged.lastTransitionTime)
        assertEquals("StillOK", merged.reason)
        assertEquals("new message", merged.message)
    }

    @Test
    fun statusFlipRefreshesLastTransitionTime() {
        val existing = Condition(
            type = "Available",
            status = "False",
            reason = "NotReady",
            message = "0/1",
            lastTransitionTime = t0.toString(),
        )
        val next = Condition(
            type = "Available",
            status = "True",
            reason = "MinimumReplicasAvailable",
            message = "1/1",
        )
        val merged = ConditionMerge.mergeCondition(existing, next, now = t1)
        assertEquals(t1.toString(), merged.lastTransitionTime)
        assertNotEquals(t0.toString(), merged.lastTransitionTime)
        assertEquals("MinimumReplicasAvailable", merged.reason)
        assertEquals("1/1", merged.message)
    }

    @Test
    fun mergeConditionsReplacesSameTypeAndPreservesOthers() {
        val existing = listOf(
            Condition("Scheduled", "True", "Placed", "", t0.toString()),
            Condition("Available", "False", "Waiting", "", t0.toString()),
        )
        val incoming = listOf(
            Condition("Available", "True", "Ready", "ok"),
        )
        val merged = ConditionMerge.mergeConditions(existing, incoming, now = t1)
        assertEquals(2, merged.size)
        assertEquals("True", merged.first { it.type == "Scheduled" }.status)
        assertEquals(t0.toString(), merged.first { it.type == "Scheduled" }.lastTransitionTime)
        assertEquals("True", merged.first { it.type == "Available" }.status)
        assertEquals(t1.toString(), merged.first { it.type == "Available" }.lastTransitionTime)
    }
}
