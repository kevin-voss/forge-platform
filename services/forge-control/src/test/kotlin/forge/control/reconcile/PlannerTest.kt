package forge.control.reconcile

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class PlannerTest {
    private val desired2 = DesiredState(
        deploymentId = "11111111-1111-1111-1111-111111111111",
        image = "registry.local/demo:v1",
        replicas = 2,
    )

    @Test
    fun equalDesiredAndActualYieldsEmptyPlan() {
        val actual = ActualState(
            listOf(
                ReplicaObservation("r1", "running"),
                ReplicaObservation("r2", "ready"),
            ),
        )
        val plan = computePlan(desired2, actual)
        assertTrue(plan.actions.isEmpty())
    }

    @Test
    fun underReplicasYieldsStartActions() {
        val actual = ActualState(listOf(ReplicaObservation("r1", "running")))
        val plan = computePlan(desired2, actual)
        assertEquals(1, plan.size)
        assertEquals(ReconcileAction.StartReplica.name, plan.actions[0].action)
        assertEquals("desired=2 actual=1", plan.actions[0].reason)
    }

    @Test
    fun overReplicasYieldsStopActions() {
        val desired1 = desired2.copy(replicas = 1)
        val actual = ActualState(
            listOf(
                ReplicaObservation("r1", "running"),
                ReplicaObservation("r2", "ready"),
            ),
        )
        val plan = computePlan(desired1, actual)
        assertEquals(1, plan.size)
        assertEquals(ReconcileAction.StopReplica.name, plan.actions[0].action)
        assertEquals("r2", plan.actions[0].replicaId)
    }

    @Test
    fun failedReplicasDoNotSatisfyDesired() {
        val desired1 = desired2.copy(replicas = 1)
        val actual = ActualState(listOf(ReplicaObservation("r1", "failed")))
        val plan = computePlan(desired1, actual)
        assertEquals(1, plan.size)
        assertEquals(ReconcileAction.StartReplica.name, plan.actions[0].action)
    }

    @Test
    fun zeroDesiredWithNoSatisfyingIsEmpty() {
        val desired0 = desired2.copy(replicas = 0)
        val actual = ActualState(listOf(ReplicaObservation("r1", "failed")))
        val plan = computePlan(desired0, actual)
        assertTrue(plan.actions.isEmpty())
    }
}
