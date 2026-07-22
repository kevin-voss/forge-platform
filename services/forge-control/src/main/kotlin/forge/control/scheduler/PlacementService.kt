package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span
import java.time.Instant
import java.util.UUID

/**
 * Orchestrates place + persist for the placement API and reconciler.
 * Idempotent for (deployment, replica_index).
 * Unplaceable requests are enqueued as pending when a [PendingQueue] is configured.
 */
class PlacementService(
    private val scheduler: Scheduler,
    private val store: PlacementStore,
    private val log: JsonLog,
    private val telemetry: Telemetry = Telemetry.current(),
    private val reservation: CapacityReservation? = null,
    private val pendingQueue: PendingQueue? = null,
    private val queueProcessor: QueueProcessor? = null,
    private val defaultAntiAffinity: AntiAffinity = AntiAffinity.Soft,
    private val idFactory: () -> String = { "plc_${UUID.randomUUID().toString().replace("-", "").take(12)}" },
    private val clock: () -> Instant = { Instant.now() },
) {
    fun placeAndPersist(
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String? = null,
        slots: Int = 1,
        antiAffinity: AntiAffinity? = null,
    ): PlaceResult {
        store.find(deploymentId, replicaIndex)?.let { existing ->
            return when (existing.status) {
                PendingQueue.STATUS_PENDING -> PlaceResult.Pending(existing, created = false)
                else -> PlaceResult.Ok(existing, created = false)
            }
        }

        val affinity = antiAffinity ?: defaultAntiAffinity
        val requirements = ResourceRequirements(slots = slots)
        val request = PlacementRequest(
            deploymentId = deploymentId.toString(),
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            requirements = requirements,
            antiAffinity = affinity,
        )
        val decision = telemetry.inSpan("scheduler.place") {
            val result = scheduler.place(request)
            val span = Span.current()
            when (result) {
                is PlacementDecision.Assigned -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), result.strategy)
                    span.setAttribute(AttributeKey.stringKey("node"), result.nodeId)
                    span.setAttribute(AttributeKey.longKey("candidates"), 1)
                }
                is PlacementDecision.NoNodeAvailable -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), "none")
                    span.setAttribute(AttributeKey.longKey("candidates"), 0)
                }
            }
            result
        }
        return when (decision) {
            is PlacementDecision.NoNodeAvailable -> {
                val queue = pendingQueue
                if (queue == null) {
                    telemetry.recordPlacementRejectedNoCapacity()
                    log.info(
                        "placement rejected no capacity",
                        "deployment_id" to deploymentId.toString(),
                        "replica_index" to replicaIndex,
                        "reason" to decision.reason,
                    )
                    return PlaceResult.NoNode(decision.reason)
                }
                try {
                    val pending = queue.enqueue(
                        deploymentId = deploymentId,
                        replicaIndex = replicaIndex,
                        reason = decision.reason,
                        slots = slots,
                        antiAffinity = affinity,
                        serviceId = serviceId,
                    )
                    telemetry.setPlacementsPending(queue.count())
                    log.info(
                        "placement enqueue",
                        "deployment_id" to deploymentId.toString(),
                        "replica_index" to replicaIndex,
                        "reason" to decision.reason,
                        "placement_id" to pending.id,
                        "anti_affinity" to affinity.wire(),
                    )
                    PlaceResult.Pending(pending, created = true)
                } catch (e: QueueFullException) {
                    telemetry.recordPlacementRejectedNoCapacity()
                    log.info(
                        "placement queue full",
                        "deployment_id" to deploymentId.toString(),
                        "replica_index" to replicaIndex,
                        "max_len" to e.maxLen,
                    )
                    PlaceResult.QueueFull(e.maxLen)
                }
            }
            is PlacementDecision.Assigned -> {
                val placement = store.upsert(
                    Placement(
                        id = idFactory(),
                        deploymentId = deploymentId,
                        replicaIndex = replicaIndex,
                        nodeId = decision.nodeId,
                        strategy = decision.strategy,
                        reason = decision.reason,
                        createdAt = clock(),
                        status = PendingQueue.STATUS_PLACED,
                        antiAffinity = affinity.wire(),
                        slots = slots,
                        serviceId = serviceId,
                    ),
                )
                log.info(
                    "placement recorded",
                    "deployment_id" to deploymentId.toString(),
                    "replica_index" to replicaIndex,
                    "node_id" to (placement.nodeId ?: ""),
                    "strategy" to placement.strategy,
                    "reason" to (placement.reason ?: ""),
                    "placement_id" to placement.id,
                    "status" to placement.status,
                )
                telemetry.recordPlacement(placement.strategy)
                telemetry.recordPlacementDecision(placement.strategy, placement.nodeId ?: "")
                PlaceResult.Ok(placement, created = true)
            }
        }
    }

    fun list(deploymentId: UUID, status: String? = null): List<Placement> =
        store.listByDeployment(deploymentId, status)

    /**
     * Delete placement and release reserved capacity. Triggers queue drain when capacity frees.
     */
    fun releasePlacement(
        deploymentId: UUID,
        replicaIndex: Int,
        slots: Int = 1,
    ): Placement? {
        val deleted = store.delete(deploymentId, replicaIndex) ?: return null
        if (deleted.status == PendingQueue.STATUS_PLACED && !deleted.nodeId.isNullOrBlank()) {
            reservation?.releaseSlots(deleted.nodeId, deleted.slots.coerceAtLeast(slots))
        }
        log.info(
            "placement released",
            "deployment_id" to deploymentId.toString(),
            "replica_index" to replicaIndex,
            "node_id" to (deleted.nodeId ?: ""),
            "placement_id" to deleted.id,
            "status" to deleted.status,
        )
        drainQueue()
        return deleted
    }

    /** Event-driven drain after capacity-freeing events (stop, new node). */
    fun drainQueue(): Int = queueProcessor?.processOnce() ?: 0
}

sealed class PlaceResult {
    data class Ok(val placement: Placement, val created: Boolean) : PlaceResult()
    data class Pending(val placement: Placement, val created: Boolean) : PlaceResult()
    data class NoNode(val reason: String) : PlaceResult()
    data class QueueFull(val maxLen: Int) : PlaceResult()
}
