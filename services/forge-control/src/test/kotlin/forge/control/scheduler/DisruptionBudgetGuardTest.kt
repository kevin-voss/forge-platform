package forge.control.scheduler

import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DisruptionBudgetGuardTest {
    private val t0 = Instant.parse("2026-07-23T12:00:00Z")
    private val dpl = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

    private fun placed(n: Int): InMemoryPlacementStore {
        val store = InMemoryPlacementStore()
        repeat(n) { i ->
            store.upsert(
                Placement(
                    id = "plc_$i",
                    deploymentId = dpl,
                    replicaIndex = i,
                    nodeId = "node-$i",
                    strategy = "first-fit",
                    reason = "seed",
                    createdAt = t0,
                ),
            )
        }
        return store
    }

    @Test
    fun minAvailableBlocksThirdVoluntaryRemovalOnTwoReplicas() {
        val placements = placed(2)
        val budgets = InMemoryDisruptionBudgetStore()
        budgets.upsert(DisruptionBudget(deploymentId = dpl, minAvailable = 2, createdAt = t0))
        val guard = DisruptionBudgetGuard(budgets, placements)
        assertFalse(guard.allowsVoluntaryRemoval(dpl).allowed)
    }

    @Test
    fun maxUnavailableAllowsExactlyOneConcurrentRemoval() {
        val placements = placed(3)
        val budgets = InMemoryDisruptionBudgetStore()
        budgets.upsert(DisruptionBudget(deploymentId = dpl, maxUnavailable = 1, createdAt = t0))
        val guard = DisruptionBudgetGuard(budgets, placements)
        // unavailable=0 → after=1 <= 1 allowed
        assertTrue(guard.allowsVoluntaryRemoval(dpl).allowed)
        // Simulate one already unavailable (pending).
        placements.upsert(
            Placement(
                id = "plc_pending",
                deploymentId = dpl,
                replicaIndex = 99,
                nodeId = null,
                strategy = "pending",
                reason = "queued",
                createdAt = t0,
                status = PendingQueue.STATUS_PENDING,
            ),
        )
        assertFalse(guard.allowsVoluntaryRemoval(dpl).allowed)
    }

    @Test
    fun noBudgetAlwaysAllows() {
        val placements = placed(1)
        val guard = DisruptionBudgetGuard(InMemoryDisruptionBudgetStore(), placements)
        assertTrue(guard.allowsVoluntaryRemoval(dpl).allowed)
    }
}
