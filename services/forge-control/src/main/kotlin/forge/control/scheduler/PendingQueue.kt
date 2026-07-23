package forge.control.scheduler

import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementAffinity
import forge.control.scheduler.model.PlacementTrace
import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.Toleration
import forge.control.scheduler.model.TopologySpreadConstraint
import java.time.Instant
import java.util.UUID

/**
 * Persisted FIFO queue of unplaceable placement requests (`status=pending`).
 */
class PendingQueue(
    private val store: PlacementStore,
    private val maxLen: Int = DEFAULT_MAX_LEN,
    private val idFactory: () -> String = {
        "plc_${UUID.randomUUID().toString().replace("-", "").take(12)}"
    },
    private val clock: () -> Instant = { Instant.now() },
) {
    init {
        require(maxLen >= 1) { "maxLen must be >= 1" }
    }

    fun count(): Int = store.countPending()

    fun listFifo(limit: Int = maxLen): List<Placement> = store.listPendingFifo(limit)

    /**
     * Enqueue a pending placement. Throws [QueueFullException] when at capacity.
     * Idempotent for (deployment, replica_index).
     */
    fun enqueue(
        deploymentId: UUID,
        replicaIndex: Int,
        reason: String,
        slots: Int = 1,
        antiAffinity: AntiAffinity = AntiAffinity.Soft,
        serviceId: String? = null,
        strategy: String = STRATEGY_PENDING,
        rescheduledFromNode: String? = null,
        requests: ResourceBundle? = null,
        limits: ResourceBundle? = null,
        trace: PlacementTrace? = null,
        nodeSelector: Map<String, String>? = null,
        tolerations: List<Toleration> = emptyList(),
        platform: PlatformSpec? = null,
        affinity: PlacementAffinity? = null,
        topologySpreadConstraints: List<TopologySpreadConstraint> = emptyList(),
        priorityClass: String = "default",
    ): Placement {
        store.find(deploymentId, replicaIndex)?.let { return it }
        if (store.countPending() >= maxLen) {
            throw QueueFullException(maxLen)
        }
        return store.upsert(
            Placement(
                id = idFactory(),
                deploymentId = deploymentId,
                replicaIndex = replicaIndex,
                nodeId = null,
                strategy = strategy.ifBlank { STRATEGY_PENDING },
                reason = reason,
                createdAt = clock(),
                status = STATUS_PENDING,
                antiAffinity = antiAffinity.wire(),
                slots = slots.coerceAtLeast(1),
                serviceId = serviceId,
                rescheduledFromNode = rescheduledFromNode,
                requests = requests,
                limits = limits,
                trace = trace,
                nodeSelector = nodeSelector,
                tolerations = tolerations,
                platform = platform,
                affinity = affinity,
                topologySpreadConstraints = topologySpreadConstraints,
                priorityClass = priorityClass.ifBlank { "default" },
            ),
        )
    }

    fun remove(deploymentId: UUID, replicaIndex: Int): Placement? =
        store.delete(deploymentId, replicaIndex)

    companion object {
        const val DEFAULT_MAX_LEN: Int = 1000
        const val STATUS_PENDING: String = "pending"
        const val STATUS_PLACED: String = "placed"
        const val STATUS_LOST: String = "lost"
        const val STRATEGY_PENDING: String = "pending"
    }
}

class QueueFullException(val maxLen: Int) :
    Exception("placement queue full (max=$maxLen)")

fun AntiAffinity.wire(): String =
    when (this) {
        AntiAffinity.Soft -> "soft"
        AntiAffinity.Hard -> "hard"
    }
