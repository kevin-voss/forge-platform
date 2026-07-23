package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.reconcile.DeploymentStore
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.NodeTaint
import forge.control.scheduler.model.TaintEffect
import forge.control.scheduler.model.Toleration
import forge.control.telemetry.Telemetry
import java.util.UUID

/**
 * On newly added [TaintEffect.NoExecute] taints, evict non-tolerating placements
 * via the same release-and-reschedule path as [NodeOfflineHandler] (08.05).
 */
class TaintChangeHandler(
    private val store: PlacementStore,
    private val placementService: PlacementService,
    private val reservation: CapacityReservation,
    private val deploymentStore: DeploymentStore?,
    private val log: JsonLog,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    /**
     * Compare previous vs next taints; evict placements that do not tolerate
     * newly introduced NoExecute taints on [nodeId].
     */
    fun onTaintsChanged(
        nodeId: String,
        previous: List<NodeTaint>,
        next: List<NodeTaint>,
        defaultTolerations: (Placement) -> List<Toleration> = { it.tolerations },
    ): Int {
        val addedNoExecute = next.filter { taint ->
            runCatching { taint.effectEnum() }.getOrNull() == TaintEffect.NoExecute &&
                previous.none { it.sameAs(taint) }
        }
        if (addedNoExecute.isEmpty()) return 0

        for (taint in addedNoExecute) {
            log.info(
                "taint added",
                "event" to "taint_added",
                "node_id" to nodeId,
                "key" to taint.key,
                "value" to (taint.value ?: ""),
                "effect" to taint.effect,
            )
        }

        val placed = store.listByNode(nodeId, PendingQueue.STATUS_PLACED)
        var evicted = 0
        for (placement in placed) {
            val tolerations = defaultTolerations(placement)
            val untolerated = TaintTolerationFilter.untoleratedTaints(addedNoExecute, tolerations)
            if (untolerated.isEmpty()) continue
            val lost = store.markLost(placement.deploymentId, placement.replicaIndex) ?: continue
            val releaseReqs = when {
                lost.requests != null && !lost.requests.isEmpty() ->
                    RequirementsResolver.resolve(
                        forge.control.scheduler.model.ResourceRequirements(
                            slots = lost.slots.coerceAtLeast(1),
                            requests = lost.requests,
                            limits = lost.limits,
                            slotsExplicit = true,
                        ),
                    ).toResourceRequirements()
                else -> forge.control.scheduler.model.ResourceRequirements(
                    slots = lost.slots.coerceAtLeast(1),
                )
            }
            reservation.release(nodeId, releaseReqs)
            telemetry.recordTaintEviction()
            log.info(
                "taint eviction",
                "event" to "taint_eviction",
                "node_id" to nodeId,
                "deployment_id" to lost.deploymentId.toString(),
                "replica_index" to lost.replicaIndex,
                "placement_id" to lost.id,
            )
            if (!stillDesired(lost.deploymentId, lost.replicaIndex)) continue
            requestReplacement(lost, fromNode = nodeId)
            evicted++
        }
        if (evicted > 0) {
            placementService.drainQueue()
        }
        return evicted
    }

    private fun stillDesired(deploymentId: UUID, replicaIndex: Int): Boolean {
        val store = deploymentStore ?: return true
        val desired = store.findDesired(deploymentId) ?: return false
        return replicaIndex in 0 until desired.replicas
    }

    private fun requestReplacement(lost: Placement, fromNode: String) {
        val affinity = try {
            AntiAffinity.parse(lost.antiAffinity)
        } catch (_: IllegalArgumentException) {
            AntiAffinity.Soft
        }
        placementService.placeAndPersist(
            deploymentId = lost.deploymentId,
            replicaIndex = lost.replicaIndex,
            serviceId = lost.serviceId,
            slots = lost.slots,
            antiAffinity = affinity,
            rescheduledFromNode = fromNode,
            requirements = when {
                lost.requests != null && !lost.requests.isEmpty() ->
                    forge.control.scheduler.model.ResourceRequirements(
                        slots = lost.slots.coerceAtLeast(1),
                        requests = lost.requests,
                        limits = lost.limits,
                        slotsExplicit = true,
                    )
                else -> null
            },
            placement = forge.control.scheduler.model.PlacementSpec(
                nodeSelector = lost.nodeSelector.orEmpty(),
                tolerations = lost.tolerations,
                affinity = lost.affinity,
                topologySpreadConstraints = lost.topologySpreadConstraints,
            ),
            platform = lost.platform,
        )
    }
}
