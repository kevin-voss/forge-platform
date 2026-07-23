package forge.control.resource

import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

class PhaseDerivationTest {
    private val now = Instant.parse("2026-07-23T12:00:00Z")

    @Test
    fun deletionTimestampYieldsTerminatingRegardlessOfConditions() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Ready", "True", "OK", "")),
            deletionTimestamp = now,
            generation = 3,
            observedGeneration = 3,
        )
        assertEquals(PhaseDerivation.Phase.Terminating, phase)
    }

    @Test
    fun failedConditionYieldsFailed() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Failed", "True", "Boom", "err")),
            deletionTimestamp = null,
            generation = 1,
            observedGeneration = 1,
        )
        assertEquals(PhaseDerivation.Phase.Failed, phase)
    }

    @Test
    fun degradedConditionYieldsDegraded() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Degraded", "True", "Partial", "")),
            deletionTimestamp = null,
            generation = 2,
            observedGeneration = 2,
        )
        assertEquals(PhaseDerivation.Phase.Degraded, phase)
    }

    @Test
    fun neverObservedYieldsPending() {
        val phase = PhaseDerivation.derivePhase(
            conditions = emptyList(),
            deletionTimestamp = null,
            generation = 1,
            observedGeneration = 0,
        )
        assertEquals(PhaseDerivation.Phase.Pending, phase)
    }

    @Test
    fun generationLagYieldsProgressing() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Available", "True", "OK", "")),
            deletionTimestamp = null,
            generation = 5,
            observedGeneration = 4,
        )
        assertEquals(PhaseDerivation.Phase.Progressing, phase)
    }

    @Test
    fun readyWhenCaughtUpAndAvailableTrue() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Available", "True", "OK", "")),
            deletionTimestamp = null,
            generation = 3,
            observedGeneration = 3,
        )
        assertEquals(PhaseDerivation.Phase.Ready, phase)
    }

    @Test
    fun progressingConditionWhenCaughtUpWithoutReady() {
        val phase = PhaseDerivation.derivePhase(
            conditions = listOf(Condition("Progressing", "True", "Rolling", "")),
            deletionTimestamp = null,
            generation = 2,
            observedGeneration = 2,
        )
        assertEquals(PhaseDerivation.Phase.Progressing, phase)
    }
}
