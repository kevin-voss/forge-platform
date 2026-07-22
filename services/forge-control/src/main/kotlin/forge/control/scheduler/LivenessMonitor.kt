package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Interval task that marks fleet nodes offline when heartbeats go stale.
 * Injectable [clock] keeps unit tests deterministic.
 */
class LivenessMonitor(
    private val store: NodeStore,
    private val timeout: Duration,
    private val intervalMs: Long,
    private val log: JsonLog,
    private val clock: Clock = Clock.systemUTC(),
    private val telemetry: Telemetry = Telemetry.current(),
    private val scheduler: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "forge-node-liveness").apply { isDaemon = true }
    },
) : AutoCloseable {
    private val running = AtomicBoolean(false)

    fun start() {
        if (!running.compareAndSet(false, true)) return
        log.info(
            "node liveness monitor starting",
            "timeout_s" to timeout.seconds,
            "interval_ms" to intervalMs,
        )
        // Recompute immediately so Control restart reflects DB heartbeats.
        evaluate()
        scheduler.scheduleWithFixedDelay(
            { safeEvaluate() },
            intervalMs.coerceAtLeast(100),
            intervalMs.coerceAtLeast(100),
            TimeUnit.MILLISECONDS,
        )
    }

    fun stop() {
        running.set(false)
        scheduler.shutdownNow()
        try {
            scheduler.awaitTermination(2, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
        log.info("node liveness monitor stopped")
    }

    override fun close() = stop()

    /** Visible for tests — one evaluation pass. */
    fun evaluate() {
        val cutoff = Instant.now(clock).minus(timeout)
        val transitions = store.recomputeLiveness(cutoff)
        for ((nodeId, status) in transitions) {
            log.info(
                "node status transition",
                "node_id" to nodeId,
                "status" to status,
                "reason" to if (status == "offline") "heartbeat_timeout" else "heartbeat_fresh",
            )
            telemetry.recordNodeStatus(status)
        }
        for (node in store.list()) {
            telemetry.recordNodeFreeSlots(node.id, freeSlots(node))
            val ageSeconds = Duration.between(node.lastHeartbeatAt, Instant.now(clock))
                .seconds
                .coerceAtLeast(0)
            telemetry.recordNodeHeartbeatAge(node.id, ageSeconds)
        }
    }

    private fun safeEvaluate() {
        if (!running.get()) return
        try {
            evaluate()
        } catch (e: Exception) {
            log.error(
                "node liveness evaluate failed",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
        }
    }

    companion object {
        fun freeSlots(node: FleetNode): Int =
            (node.capacity.slots - node.allocation.slots).coerceAtLeast(0)
    }
}
