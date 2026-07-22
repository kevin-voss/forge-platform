package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.reconcile.DeploymentEvent
import forge.control.reconcile.DeploymentHistory
import forge.control.reconcile.DeploymentStore
import forge.control.scheduler.model.AntiAffinity
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Reacts to node online→offline transitions: after a grace period, marks that
 * node's placements lost, frees capacity, and requests fresh placements for
 * still-desired replicas (placed or pending via 08.03/08.04).
 */
class NodeOfflineHandler(
    private val store: PlacementStore,
    private val placementService: PlacementService,
    private val reservation: CapacityReservation,
    private val deploymentStore: DeploymentStore,
    private val log: JsonLog,
    private val enabled: Boolean = true,
    private val grace: Duration = Duration.ofSeconds(5),
    private val history: DeploymentHistory? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val clock: Clock = Clock.systemUTC(),
    private val nodeStore: NodeStore? = null,
    private val scheduler: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "forge-node-offline").apply { isDaemon = true }
    },
) : AutoCloseable {
    private val running = AtomicBoolean(false)
    private val pendingGrace = ConcurrentHashMap<String, ScheduledFuture<*>>()

    fun start() {
        if (!enabled) {
            log.info("node offline handler disabled", "enabled" to false)
            return
        }
        if (!running.compareAndSet(false, true)) return
        log.info(
            "node offline handler starting",
            "grace_s" to grace.seconds,
            "enabled" to true,
        )
        recoverLostReplicas()
    }

    fun stop() {
        running.set(false)
        pendingGrace.values.forEach { it.cancel(false) }
        pendingGrace.clear()
        scheduler.shutdownNow()
        try {
            scheduler.awaitTermination(2, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
        log.info("node offline handler stopped")
    }

    override fun close() = stop()

    /** Hook for [LivenessMonitor] status transitions. */
    fun onStatusTransition(nodeId: String, status: String) {
        if (!enabled) return
        when (status) {
            "offline" -> scheduleReschedule(nodeId)
            "online" -> cancelGrace(nodeId)
        }
    }

    /** Visible for tests — cancel grace / flap suppression. */
    fun cancelGrace(nodeId: String) {
        pendingGrace.remove(nodeId)?.cancel(false)
    }

    /** Visible for tests — run reschedule immediately (bypasses grace). */
    fun handleOfflineNow(nodeId: String): Int = rescheduleNode(nodeId)

    /**
     * Idempotent recovery after Control restart: lost rows without an active
     * replacement are re-placed if still desired.
     */
    fun recoverLostReplicas(): Int {
        if (!enabled) return 0
        var count = 0
        for (lost in store.listLostWithoutActive()) {
            if (!stillDesired(lost.deploymentId, lost.replicaIndex)) continue
            if (requestReplacement(lost, fromNode = lost.nodeId ?: "unknown")) {
                count++
            }
        }
        return count
    }

    private fun scheduleReschedule(nodeId: String) {
        cancelGrace(nodeId)
        val delayMs = grace.toMillis().coerceAtLeast(0)
        if (delayMs == 0L) {
            rescheduleNode(nodeId)
            return
        }
        val future = scheduler.schedule(
            {
                pendingGrace.remove(nodeId)
                rescheduleNode(nodeId)
            },
            delayMs,
            TimeUnit.MILLISECONDS,
        )
        pendingGrace[nodeId] = future
    }

    private fun rescheduleNode(nodeId: String): Int =
        telemetry.inSpan("scheduler.reschedule") {
            Span.current().setAttribute(AttributeKey.stringKey("node"), nodeId)
            // Idempotent: liveness usually already flipped status; ensure the node
            // cannot win replacement placements while we free its capacity.
            nodeStore?.markOffline(nodeId)
            val placed = store.listByNode(nodeId, PendingQueue.STATUS_PLACED)
            log.info(
                "node offline",
                "event" to "node_offline",
                "node" to nodeId,
                "lost_replicas" to placed.size,
            )
            telemetry.recordNodeOffline()

            var rescheduled = 0
            for (placement in placed) {
                val lost = store.markLost(placement.deploymentId, placement.replicaIndex) ?: continue
                reservation.releaseSlots(nodeId, lost.slots.coerceAtLeast(1))
                if (!stillDesired(lost.deploymentId, lost.replicaIndex)) continue
                if (requestReplacement(lost, fromNode = nodeId)) {
                    rescheduled++
                }
            }
            placementService.drainQueue()
            Span.current().setAttribute(AttributeKey.longKey("rescheduled"), rescheduled.toLong())
            rescheduled
        }

    private fun stillDesired(deploymentId: UUID, replicaIndex: Int): Boolean {
        val desired = deploymentStore.findDesired(deploymentId) ?: return false
        return replicaIndex in 0 until desired.replicas
    }

    private fun requestReplacement(lost: Placement, fromNode: String): Boolean {
        val affinity = try {
            AntiAffinity.parse(lost.antiAffinity)
        } catch (_: IllegalArgumentException) {
            AntiAffinity.Soft
        }
        val result = placementService.placeAndPersist(
            deploymentId = lost.deploymentId,
            replicaIndex = lost.replicaIndex,
            serviceId = lost.serviceId,
            slots = lost.slots,
            antiAffinity = affinity,
            rescheduledFromNode = fromNode,
        )
        val (toNode, resultLabel) = when (result) {
            is PlaceResult.Ok -> result.placement.nodeId to "placed"
            is PlaceResult.Pending -> null to "pending"
            is PlaceResult.NoNode -> null to "pending"
            is PlaceResult.QueueFull -> null to "pending"
        }
        telemetry.recordReschedule(resultLabel)
        log.info(
            "replica reschedule",
            "event" to "reschedule",
            "replica" to lost.replicaIndex,
            "deployment_id" to lost.deploymentId.toString(),
            "from_node" to fromNode,
            "to_node" to (toNode ?: "pending"),
            "result" to resultLabel,
        )
        recordHistory(lost.deploymentId, lost.replicaIndex, fromNode, toNode)
        return result is PlaceResult.Ok || result is PlaceResult.Pending
    }

    private fun recordHistory(
        deploymentId: UUID,
        replicaIndex: Int,
        fromNode: String,
        toNode: String?,
    ) {
        val hist = history ?: return
        val desired = deploymentStore.findDesired(deploymentId)
        val status = deploymentStore.getStatus(deploymentId) ?: "deployed"
        hist.append(
            DeploymentEvent(
                id = 0,
                deploymentId = deploymentId,
                at = Instant.now(clock),
                fromStatus = status,
                toStatus = status,
                image = desired?.image,
                desiredReplicas = desired?.replicas,
                actualReplicas = null,
                reason = "rescheduled: replica=$replicaIndex from=$fromNode to=${toNode ?: "pending"}",
            ),
        )
    }
}
