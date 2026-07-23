package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span
import java.time.Instant
import java.util.UUID

/**
 * Orchestrates place + persist for the placement API and reconciler.
 * Idempotent for (deployment, replica_index).
 * Unplaceable requests try preemption (when enabled) before the pending queue.
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
    private val priorityClasses: PriorityClassStore = InMemoryPriorityClassStore(),
    private val defaultPriorityClass: String = JdbcPriorityClassStore.DEFAULT_NAME,
    private val preemptionEnabled: Boolean = true,
    private val preemptionSelector: PreemptionSelector? = null,
    private val gracefulEvictor: GracefulEvictor? = null,
    private val preemptionAuditor: PreemptionAuditor? = null,
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
        placement: PlacementSpec = PlacementSpec(),
        platform: PlatformSpec? = null,
        priorityClass: String? = null,
        allowPreemption: Boolean = true,
    ): PlaceResult {
        store.find(deploymentId, replicaIndex)?.let { existing ->
            return when (existing.status) {
                PendingQueue.STATUS_PENDING -> PlaceResult.Pending(existing, created = false)
                else -> PlaceResult.Ok(existing, created = false)
            }
        }

        val affinity = antiAffinity ?: defaultAntiAffinity
        val resolvedClass = priorityClasses.resolve(
            priorityClass?.trim()?.takeIf { it.isNotEmpty() } ?: defaultPriorityClass,
        )
        val resolvedReqs = RequirementsResolver.resolve(
            requirements ?: ResourceRequirements(slots = slots, slotsExplicit = true),
        )
        val request = PlacementRequest(
            deploymentId = deploymentId.toString(),
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            requirements = resolvedReqs.toResourceRequirements(),
            antiAffinity = affinity,
            placement = placement,
            platform = platform,
            priorityClass = resolvedClass.name,
        )
        var decision = placeWithTelemetry(request, resolvedReqs)

        if (decision is PlacementDecision.NoNodeAvailable &&
            allowPreemption &&
            preemptionEnabled &&
            resolvedClass.preemptionPolicy == PreemptionPolicy.PreemptLowerPriority
        ) {
            val preempted = tryPreempt(
                request = request,
                preemptorClass = resolvedClass,
                deploymentId = deploymentId,
                replicaIndex = replicaIndex,
                serviceId = serviceId,
                affinity = affinity,
                resolvedReqs = resolvedReqs,
                rescheduledFromNode = rescheduledFromNode,
                placement = placement,
                platform = platform,
            )
            if (preempted != null) return preempted
            decision = placeWithTelemetry(request, resolvedReqs)
        }

        return when (decision) {
            is PlacementDecision.NoNodeAvailable ->
                enqueueOrReject(
                    decision = decision,
                    deploymentId = deploymentId,
                    replicaIndex = replicaIndex,
                    serviceId = serviceId,
                    affinity = affinity,
                    resolvedReqs = resolvedReqs,
                    rescheduledFromNode = rescheduledFromNode,
                    placement = placement,
                    platform = platform,
                    priorityClassName = resolvedClass.name,
                )
            is PlacementDecision.Assigned ->
                persistAssigned(
                    decision = decision,
                    deploymentId = deploymentId,
                    replicaIndex = replicaIndex,
                    serviceId = serviceId,
                    affinity = affinity,
                    resolvedReqs = resolvedReqs,
                    rescheduledFromNode = rescheduledFromNode,
                    placement = placement,
                    platform = platform,
                    priorityClassName = resolvedClass.name,
                )
        }
    }

    /** Used by [GracefulEvictor] after victim capacity is freed. */
    fun resubmitFromLost(lost: Placement): PlaceResult {
        val affinity = try {
            AntiAffinity.parse(lost.antiAffinity)
        } catch (_: IllegalArgumentException) {
            AntiAffinity.Soft
        }
        return placeAndPersist(
            deploymentId = lost.deploymentId,
            replicaIndex = lost.replicaIndex,
            serviceId = lost.serviceId,
            slots = lost.slots,
            antiAffinity = affinity,
            rescheduledFromNode = lost.nodeId,
            requirements = when {
                lost.requests != null && !lost.requests.isEmpty() ->
                    ResourceRequirements(
                        slots = lost.slots.coerceAtLeast(1),
                        requests = lost.requests,
                        limits = lost.limits,
                        slotsExplicit = true,
                    )
                else -> null
            },
            placement = PlacementSpec(
                nodeSelector = lost.nodeSelector.orEmpty(),
                tolerations = lost.tolerations,
                affinity = lost.affinity,
                topologySpreadConstraints = lost.topologySpreadConstraints,
            ),
            platform = lost.platform,
            priorityClass = lost.priorityClass,
            allowPreemption = false,
        )
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

    private fun tryPreempt(
        request: PlacementRequest,
        preemptorClass: PriorityClass,
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String?,
        affinity: AntiAffinity,
        resolvedReqs: ResolvedRequirements,
        rescheduledFromNode: String?,
        placement: PlacementSpec,
        platform: PlatformSpec?,
    ): PlaceResult? {
        val selector = preemptionSelector ?: return null
        val evictor = gracefulEvictor ?: return null
        val selection = selector.findMinimalVictims(request, preemptorClass) ?: return null
        val preemptorId = idFactory()

        val released = mutableListOf<Placement>()
        for (victim in selection.victims) {
            val lost = evictor.releaseVictim(victim, preemptorId) ?: continue
            released += lost
            val victimPriority = priorityClasses.resolve(victim.priorityClass).value
            log.info(
                "preemption",
                "event" to "preemption",
                "victim_placement" to victim.id,
                "preemptor_placement" to preemptorId,
                "victim_priority" to victimPriority,
                "preemptor_priority" to preemptorClass.value,
                "node" to selection.nodeId,
            )
            telemetry.recordPreemption(victimPriority, preemptorClass.value)
            preemptionAuditor?.record(
                PreemptionEvent(
                    id = "",
                    victimPlacementId = victim.id,
                    preemptorPlacementId = preemptorId,
                    victimPriority = victimPriority,
                    preemptorPriority = preemptorClass.value,
                    nodeId = selection.nodeId,
                    reason = "preempted: insufficient capacity for priority " +
                        "${preemptorClass.value} request",
                    createdAt = clock(),
                    victimDeploymentId = victim.deploymentId,
                ),
            )
        }
        if (released.isEmpty()) return null

        val decision = placeWithTelemetry(request, resolvedReqs)
        if (decision !is PlacementDecision.Assigned) {
            // Capacity still insufficient after eviction — resubmit victims and fall through.
            for (lost in released) {
                evictor.resubmitVictim(lost)
            }
            return null
        }

        val placed = persistAssigned(
            decision = decision,
            deploymentId = deploymentId,
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            affinity = affinity,
            resolvedReqs = resolvedReqs,
            rescheduledFromNode = rescheduledFromNode,
            placement = placement,
            platform = platform,
            priorityClassName = preemptorClass.name,
            placementId = preemptorId,
            reasonOverride = "preempted ${released.size} victim(s) on ${selection.nodeId}; " +
                decision.reason,
        )
        for (lost in released) {
            evictor.resubmitVictim(lost)
        }
        return placed
    }

    private fun placeWithTelemetry(
        request: PlacementRequest,
        resolvedReqs: ResolvedRequirements,
    ): PlacementDecision =
        telemetry.inSpan("scheduler.place") {
            val result = scheduler.place(request)
            val span = Span.current()
            resolvedReqs.cpuMillis?.let {
                span.setAttribute(AttributeKey.longKey("requested_cpu_millis"), it.toLong())
            }
            resolvedReqs.memoryMb?.let {
                span.setAttribute(AttributeKey.longKey("requested_memory_mb"), it.toLong())
            }
            val decisionTrace = when (result) {
                is PlacementDecision.Assigned -> result.trace
                is PlacementDecision.NoNodeAvailable -> result.trace
            }
            decisionTrace?.filterNames()?.let { names ->
                span.setAttribute(AttributeKey.stringArrayKey("filters_applied"), names)
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
                    decisionTrace?.filters?.forEach { filter ->
                        if (filter.eliminated.isNotEmpty()) {
                            telemetry.recordPlacementFiltered(filter.name)
                        }
                    }
                    result.unschedulableReasons.forEach { entry ->
                        telemetry.recordPlacementUnschedulable(entry.reason)
                    }
                }
            }
            result
        }

    private fun enqueueOrReject(
        decision: PlacementDecision.NoNodeAvailable,
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String?,
        affinity: AntiAffinity,
        resolvedReqs: ResolvedRequirements,
        rescheduledFromNode: String?,
        placement: PlacementSpec,
        platform: PlatformSpec?,
        priorityClassName: String,
    ): PlaceResult {
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
        return try {
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
                nodeSelector = placement.nodeSelector.takeIf { it.isNotEmpty() },
                tolerations = placement.tolerations,
                platform = platform,
                affinity = placement.affinity,
                topologySpreadConstraints = placement.topologySpreadConstraints,
                priorityClass = priorityClassName,
            )
            telemetry.setPlacementsPending(queue.count())
            log.info(
                "placement enqueue",
                "deployment_id" to deploymentId.toString(),
                "replica_index" to replicaIndex,
                "reason" to decision.reason,
                "placement_id" to pending.id,
                "anti_affinity" to affinity.wire(),
                "priority_class" to priorityClassName,
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

    private fun persistAssigned(
        decision: PlacementDecision.Assigned,
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String?,
        affinity: AntiAffinity,
        resolvedReqs: ResolvedRequirements,
        rescheduledFromNode: String?,
        placement: PlacementSpec,
        platform: PlatformSpec?,
        priorityClassName: String,
        placementId: String = idFactory(),
        reasonOverride: String? = null,
    ): PlaceResult.Ok {
        val recorded = store.upsert(
            Placement(
                id = placementId,
                deploymentId = deploymentId,
                replicaIndex = replicaIndex,
                nodeId = decision.nodeId,
                strategy = decision.strategy,
                reason = reasonOverride ?: decision.reason,
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
                nodeSelector = placement.nodeSelector.takeIf { it.isNotEmpty() },
                tolerations = placement.tolerations,
                platform = platform,
                affinity = placement.affinity,
                topologySpreadConstraints = placement.topologySpreadConstraints,
                priorityClass = priorityClassName,
            ),
        )
        log.info(
            "placement recorded",
            "deployment_id" to deploymentId.toString(),
            "replica_index" to replicaIndex,
            "node_id" to (recorded.nodeId ?: ""),
            "strategy" to recorded.strategy,
            "reason" to (recorded.reason ?: ""),
            "placement_id" to recorded.id,
            "status" to recorded.status,
            "priority_class" to priorityClassName,
            "requested" to resolvedReqs.requests.toString(),
            "chosen_node" to decision.nodeId,
        )
        telemetry.recordPlacement(recorded.strategy)
        telemetry.recordPlacementDecision(recorded.strategy, recorded.nodeId ?: "")
        return PlaceResult.Ok(recorded, created = true)
    }
}

sealed class PlaceResult {
    data class Ok(val placement: Placement, val created: Boolean) : PlaceResult()
    data class Pending(val placement: Placement, val created: Boolean) : PlaceResult()
    data class NoNode(val reason: String) : PlaceResult()
    data class QueueFull(val maxLen: Int) : PlaceResult()
}
