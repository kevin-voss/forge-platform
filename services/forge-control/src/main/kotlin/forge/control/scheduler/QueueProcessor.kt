package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Retries pending placements in FIFO order on each scheduler tick and on
 * capacity-freeing events.
 */
class QueueProcessor(
    private val queue: PendingQueue,
    private val scheduler: Scheduler,
    private val store: PlacementStore,
    private val log: JsonLog,
    private val intervalMs: Long,
    private val telemetry: Telemetry = Telemetry.current(),
    private val executor: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "forge-placement-queue").apply { isDaemon = true }
    },
) : AutoCloseable {
    private val running = AtomicBoolean(false)

    fun start() {
        if (!running.compareAndSet(false, true)) return
        log.info("placement queue processor starting", "interval_ms" to intervalMs)
        processOnce()
        executor.scheduleWithFixedDelay(
            { safeProcess() },
            intervalMs.coerceAtLeast(50),
            intervalMs.coerceAtLeast(50),
            TimeUnit.MILLISECONDS,
        )
    }

    fun stop() {
        running.set(false)
        executor.shutdownNow()
        try {
            executor.awaitTermination(2, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
        log.info("placement queue processor stopped")
    }

    override fun close() = stop()

    /** Visible for tests / event-driven drain. */
    fun processOnce(): Int =
        telemetry.inSpan("scheduler.queue.process") {
            Span.current().setAttribute(
                AttributeKey.longKey("pending"),
                queue.count().toLong(),
            )
            var drained = 0
            for (pending in queue.listFifo()) {
                if (tryPlace(pending)) {
                    drained++
                    telemetry.recordQueueDrain()
                    log.info(
                        "placement dequeue",
                        "deployment_id" to pending.deploymentId.toString(),
                        "replica_index" to pending.replicaIndex,
                        "placement_id" to pending.id,
                        "reason" to "placed_from_queue",
                    )
                }
            }
            telemetry.setPlacementsPending(queue.count())
            drained
        }

    private fun tryPlace(pending: Placement): Boolean {
        if (pending.status != PendingQueue.STATUS_PENDING) return false
        val requirements = when {
            pending.requests != null && !pending.requests.isEmpty() ->
                ResourceRequirements(
                    slots = pending.slots.coerceAtLeast(1),
                    requests = pending.requests,
                    limits = pending.limits,
                    slotsExplicit = true,
                )
            else -> ResourceRequirements(slots = pending.slots.coerceAtLeast(1), slotsExplicit = true)
        }
        val request = PlacementRequest(
            deploymentId = pending.deploymentId.toString(),
            replicaIndex = pending.replicaIndex,
            serviceId = pending.serviceId,
            requirements = requirements,
            antiAffinity = AntiAffinity.parse(pending.antiAffinity),
        )
        return when (val decision = scheduler.place(request)) {
            is PlacementDecision.NoNodeAvailable -> false
            is PlacementDecision.Assigned -> {
                store.markPlaced(
                    deploymentId = pending.deploymentId,
                    replicaIndex = pending.replicaIndex,
                    nodeId = decision.nodeId,
                    strategy = decision.strategy,
                    reason = decision.reason,
                    trace = decision.trace,
                )
                true
            }
        }
    }

    private fun safeProcess() {
        if (!running.get()) return
        try {
            processOnce()
        } catch (e: Exception) {
            log.error(
                "placement queue process failed",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
        }
    }
}
