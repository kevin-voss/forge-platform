package forge.control.reconcile

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlinx.serialization.json.Json

class ReconcileSnapshotSerializationTest {
    private val json = Json { encodeDefaults = true; ignoreUnknownKeys = true }

    @Test
    fun desiredActualPlanRoundTrip() {
        val desired = DesiredState(
            deploymentId = "11111111-1111-1111-1111-111111111111",
            image = "registry.local/demo:v1",
            replicas = 2,
            rollout = RolloutPolicy(batchSize = 1, timeoutSeconds = 120),
        )
        val actual = ActualState(listOf(ReplicaObservation("r1", "running")))
        val plan = ReconcilePlan(
            listOf(
                ReconcileActionItem(
                    action = ReconcileAction.StartReplica.name,
                    reason = "desired=2 actual=1",
                ),
            ),
        )

        val desiredDecoded = json.decodeFromString(
            DesiredState.serializer(),
            json.encodeToString(DesiredState.serializer(), desired),
        )
        val actualDecoded = json.decodeFromString(
            ActualState.serializer(),
            json.encodeToString(ActualState.serializer(), actual),
        )
        val planDecoded = json.decodeFromString(
            ReconcilePlan.serializer(),
            json.encodeToString(ReconcilePlan.serializer(), plan),
        )

        assertEquals(desired, desiredDecoded)
        assertEquals(actual, actualDecoded)
        assertEquals(plan, planDecoded)
    }
}
