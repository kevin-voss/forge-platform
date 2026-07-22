package forge.control.reconcile

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RollingPlannerTest {
    private val deploymentId = "11111111-1111-1111-1111-111111111111"
    private val v1 = "registry.local/demo:v1"
    private val v2 = "registry.local/demo:v2"

    private fun desired(
        image: String = v2,
        replicas: Int = 2,
        batchSize: Int = 1,
    ) = DesiredState(
        deploymentId = deploymentId,
        image = image,
        replicas = replicas,
        rollout = RolloutPolicy(batchSize = batchSize, timeoutSeconds = 120),
    )

    private fun replica(
        index: Int,
        image: String,
        status: String = "ready",
    ) = ReplicaObservation(
        replicaId = index.toString(),
        status = status,
        replicaIndex = index,
        image = image,
    )

    @Test
    fun rollingPlanV1V1ToV2Batch1IsFiveStepSequenceOneReplicaAtATime() {
        val actual = ActualState(listOf(replica(0, v1), replica(1, v1)))
        val plan = computeRollingPlan(desired(batchSize = 1), actual)

        assertEquals(
            listOf(
                ReconcileAction.StartReplica.name,
                ReconcileAction.WaitReady.name,
                ReconcileAction.ShiftTraffic.name,
                ReconcileAction.DrainReplica.name,
                ReconcileAction.StopReplica.name,
            ),
            plan.actions.map { it.action },
        )
        assertEquals(1, plan.actions.count { it.action == ReconcileAction.StartReplica.name })
        assertEquals(1, plan.actions.count { it.action == ReconcileAction.StopReplica.name })
        assertEquals(RolloutPhase.Rolling.wire(), plan.phase)
        assertEquals(0, plan.updatedReplicas)
        assertEquals(2, plan.totalReplicas)
        assertEquals(v1, plan.currentImage)
        assertEquals(v2, plan.targetImage)
    }

    @Test
    fun batchSize2StartsBothBeforeStops() {
        val actual = ActualState(listOf(replica(0, v1), replica(1, v1)))
        val plan = computeRollingPlan(desired(batchSize = 2), actual)
        val actions = plan.actions.map { it.action }

        val firstStop = actions.indexOf(ReconcileAction.StopReplica.name)
        val lastStart = actions.indexOfLast { it == ReconcileAction.StartReplica.name }
        assertTrue(firstStop > lastStart, "starts must precede stops: $actions")
        assertEquals(2, actions.count { it == ReconcileAction.StartReplica.name })
        assertEquals(2, actions.count { it == ReconcileAction.StopReplica.name })
    }

    @Test
    fun plannerNeverEmitsStopBelowMinAvailable() {
        val actual = ActualState(listOf(replica(0, v1), replica(1, v1)))
        val plan = computeRollingPlan(desired(replicas = 2, batchSize = 1), actual)
        // projected ready after start+ready = 3; minAvailable = 1; one stop → ready 2 >= 1
        var ready = 3
        val minAvailable = 1
        for (action in plan.actions) {
            if (action.action == ReconcileAction.StopReplica.name) {
                assertTrue(ready - 1 >= minAvailable)
                ready--
            }
        }
    }

    @Test
    fun pendingNewReplicaYieldsWaitReadyOnly() {
        val actual = ActualState(
            listOf(
                replica(0, v1),
                replica(1, v1),
                replica(2, v2, status = "running"),
            ),
        )
        val plan = computeRollingPlan(desired(batchSize = 1), actual)
        assertEquals(listOf(ReconcileAction.WaitReady.name), plan.actions.map { it.action })
        assertEquals("2", plan.actions.single().replicaId)
        assertFalse(plan.actions.any { it.action == ReconcileAction.StopReplica.name })
    }

    @Test
    fun readyNewContinuesWithShiftDrainStop() {
        val actual = ActualState(
            listOf(
                replica(0, v1),
                replica(1, v1),
                replica(2, v2, status = "ready"),
            ),
        )
        val plan = computeRollingPlan(desired(batchSize = 1), actual)
        assertEquals(
            listOf(
                ReconcileAction.ShiftTraffic.name,
                ReconcileAction.DrainReplica.name,
                ReconcileAction.StopReplica.name,
            ),
            plan.actions.map { it.action },
        )
        assertEquals("0", plan.actions.last().replicaId)
    }

    @Test
    fun sameImageUsesSingleVersionPlan() {
        val actual = ActualState(listOf(replica(0, v1)))
        val plan = computeReconcilePlan(desired(image = v1, replicas = 2), actual)
        assertEquals(1, plan.size)
        assertEquals(ReconcileAction.StartReplica.name, plan.actions[0].action)
        assertEquals(RolloutPhase.Converging.wire(), plan.phase)
        assertFalse(needsRollingUpdate(desired(image = v1, replicas = 2), actual))
    }

    @Test
    fun nullImagesDoNotTriggerRolling() {
        val actual = ActualState(
            listOf(
                ReplicaObservation("0", "running", replicaIndex = 0),
                ReplicaObservation("1", "ready", replicaIndex = 1),
            ),
        )
        assertFalse(needsRollingUpdate(desired(image = v2, replicas = 2), actual))
    }
}
