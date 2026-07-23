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
        requirements: ResourceRequirements? = null,
        antiAffinity: AntiAffinity? = null,
        rescheduledFromNode: String? = null,
    ): PlaceResult {
        store.find(deploymentId, replicaIndex)?.let { existing ->
            return when (existing.status) {
                PendingQueue.STATUS_PENDING -> PlaceResult.Pending(existing, created = false)
                else -> PlaceResult.Ok(existing, created = false)
            }
        }

        val affinity = antiAffinity ?: defaultAntiAffinity
        val resolvedReqs = RequirementsResolver.resolve(
            requirements ?: ResourceRequirements(slots = slots, slotsExplicit = true),
        )
        val request = PlacementRequest(
            deploymentId = deploymentId.toString(),
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            requirements = resolvedReqs.toResourceRequirements(),
            antiAffinity = affinity,
        )
        val decision = telemetry.inSpan("scheduler.place") {
            val result = scheduler.place(request)
            val span = Span.current()
            resolvedReqs.cpuMillis?.let {
                span.setAttribute(AttributeKey.longKey("requested_cpu_millis"), it.toLong())
            }
            resolvedReqs.memoryMb?.let {
                span.setAttribute(AttributeKey.longKey("requested_memory_mb"), it.toLong())
            }
            when (result) {
                is PlacementDecision.Assigned -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), result.strategy)
                    span.setAttribute(AttributeKey.stringKey("node"), result.nodeId)
                    span.setAttribute(AttributeKey.longKey("candidates"), 1)
                }
                is PlacementDecision.NoNodeAvailable -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), "none")
                    span.setAttribute(AttributeKey.longKey("candidates"), 0)
                    result.unschedulableReasons.forEach { entry ->
                        telemetry.recordPlacementUnschedulable(entry.reason)
                    }
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
                        "requested" to resolvedReqs.requests.toString(),
                    )
                    return PlaceResult.NoNode(decision.reason)
                }
                try {
                    val pending = queue.enqueue(
                        deploymentId = deploymentId,
                        replicaIndex = replicaIndex,
                        reason = decision.reason,
                        slots = resolvedReqs.slots,
                        antiAffinity = affinity,
                        serviceId = serviceId,
                        rescheduledFromNode = rescheduledFromNode,
                        requests = resolvedReqs.requests
                            .takeIf { resolvedReqs.requestsAuthoritative && !it.isEmpty() },
                        limits = resolvedReqs.limits,
                        trace = decision.trace,
                    )
                    telemetry.setPlacementsPending(queue.count())
                    log.info(
                        "placement enqueue",
                        "deployment_id" to deploymentId.toString(),
                        "replica_index" to replicaIndex,
                        "reason" to decision.reason,
                        "placement_id" to pending.id,
                        "anti_affinity" to affinity.wire(),
                        "requested" to resolvedReqs.requests.toString(),
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
                        slots = resolvedReqs.slots,
                        serviceId = serviceId,
                        rescheduledFromNode = rescheduledFromNode,
                        requests = resolvedReqs.requests
                            .takeIf { resolvedReqs.requestsAuthoritative && !it.isEmpty() },
                        limits = resolvedReqs.limits,
                        trace = decision.trace,
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
                    "requested" to resolvedReqs.requests.toString(),
                    "chosen_node" to decision.nodeId,
                )
                telemetry.recordPlacement(placement.strategy)
                telemetry.recordPlacementDecision(placement.strategy, placement.nodeId ?: "")
                PlaceResult.Ok(placement, created = true)
            }
        }
    }

    fun get(id: String): Placement? = store.findById(id)

    fun list(deploymentId: UUID, status: String? = null): List<Placement> =
        store.listByDeployment(deploymentId, status)

    /** Cluster-wide pending placements (FIFO), for node autoscaler demand signals. */
    fun listPending(limit: Int = 1000): List<Placement> =
        store.listPendingFifo(limit)

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
            val releaseReqs = when {
                deleted.requests != null && !deleted.requests.isEmpty() ->
                    RequirementsResolver.resolve(
                        ResourceRequirements(
                            slots = deleted.slots.coerceAtLeast(slots),
                            requests = deleted.requests,
                            limits = deleted.limits,
                            slotsExplicit = true,
                        ),
                    ).toResourceRequirements()
                else -> ResourceRequirements(slots = deleted.slots.coerceAtLeast(slots))
            }
            reservation?.release(deleted.nodeId, releaseReqs)
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

    /**
     * Release active placements whose replica index is at/above [desiredReplicas].
     *
     * Needed when observation already lost surplus replicas (so the planner never
     * emits StopReplica) but CapacityReservation still holds their slots. Heartbeats
     * never shrink reserved slots, so without this scale-down leaves every node full
     * and the node autoscaler cannot find underutilized victims.
     */
    fun releaseOrphanedAboveDesired(deploymentId: UUID, desiredReplicas: Int): Int {
        val floor = desiredReplicas.coerceAtLeast(0)
        val orphans = store.listByDeployment(deploymentId).filter { it.replicaIndex >= floor }
        var released = 0
        for (orphan in orphans.sortedByDescending { it.replicaIndex }) {
            if (releasePlacement(deploymentId, orphan.replicaIndex, orphan.slots) != null) {
                released++
            }
        }
        if (released > 0) {
            log.info(
                "orphaned placements released",
                "deployment_id" to deploymentId.toString(),
                "desired_replicas" to floor,
                "released" to released,
            )
        }
        return released
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
