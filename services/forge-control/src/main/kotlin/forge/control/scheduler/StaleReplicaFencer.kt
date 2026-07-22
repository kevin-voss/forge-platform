package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.reconcile.ActualState
import forge.control.reconcile.DesiredState
import forge.control.reconcile.ReplicaObservation
import forge.control.reconcile.ReplicaStatus
import forge.control.reconcile.RuntimeClient
import forge.control.reconcile.WorkloadNamer
import forge.control.telemetry.Telemetry
import java.util.UUID

/**
 * On node recovery (or any tick where actual exceeds desired), stop surplus
 * replicas — preferring indices that have a [PendingQueue.STATUS_LOST] placement
 * so a flapping node cannot silently double replica counts.
 */
class StaleReplicaFencer(
    private val store: PlacementStore,
    private val runtimeClient: RuntimeClient,
    private val log: JsonLog,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    /**
     * Stops surplus satisfying replicas until actual <= desired.
     * Returns the replica indices that were fenced.
     */
    fun fence(desired: DesiredState, actual: ActualState): List<Int> {
        val satisfying = actual.replicas.filter { it.statusEnum() in SATISFYING }
        val surplus = satisfying.size - desired.replicas
        if (surplus <= 0) return emptyList()

        val deploymentId = UUID.fromString(desired.deploymentId)
        val lostIndices = store.listByDeployment(deploymentId, PendingQueue.STATUS_LOST)
            .map { it.replicaIndex }
            .toSet()

        val stopTargets = satisfying
            .sortedWith(
                compareByDescending<ReplicaObservation> {
                    val idx = it.resolvedIndex()
                    idx != null && idx in lostIndices
                }.thenByDescending { it.resolvedIndex() ?: Int.MIN_VALUE },
            )
            .take(surplus)

        val fenced = mutableListOf<Int>()
        for (replica in stopTargets) {
            val index = replica.resolvedIndex() ?: continue
            val runtimeId = WorkloadNamer.runtimeDeploymentId(
                desired.serviceSlug,
                deploymentId,
                index,
            )
            // Stop the surplus workload. Lost placements already released capacity;
            // do not delete an active replacement placement for the same index.
            runtimeClient.stopWorkload(runtimeId)
            telemetry.recordStaleReplicaFenced()
            log.info(
                "stale replica fenced",
                "deployment_id" to desired.deploymentId,
                "replica_index" to index,
                "runtime_id" to runtimeId,
                "reason" to "exceeds_desired",
            )
            fenced += index
        }
        return fenced
    }

    companion object {
        private val SATISFYING = setOf(
            ReplicaStatus.Pending,
            ReplicaStatus.Running,
            ReplicaStatus.Ready,
        )
    }
}
