package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Clock
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Interval reconciliation loop. Loads desired + actual, computes a plan, logs it,
 * and persists a status snapshot. Does **not** execute start/stop actions (07.01).
 */
class ReconciliationController(
    private val deploymentStore: DeploymentStore,
    private val runtimeClient: RuntimeClient,
    private val statusStore: ReconcileStatusStore,
    private val log: JsonLog,
    private val intervalMs: Long,
    private val enabled: Boolean,
    private val clock: Clock = Clock.systemUTC(),
    private val telemetry: Telemetry = Telemetry.current(),
    private val scheduler: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "forge-reconcile").apply { isDaemon = true }
    },
) : AutoCloseable {
    private val running = AtomicBoolean(false)

    fun start() {
        if (!enabled) {
            log.info("reconcile controller disabled", "enabled" to false)
            return
        }
        if (!running.compareAndSet(false, true)) return
        log.info(
            "reconcile controller starting",
            "interval_ms" to intervalMs,
            "enabled" to true,
        )
        scheduler.scheduleWithFixedDelay(
            { safeTickAll() },
            0L,
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
        log.info("reconcile controller stopped")
    }

    override fun close() = stop()

    /** Visible for tests — one full pass over all deployments. */
    fun tickAll() {
        val deployments = try {
            deploymentStore.listDesired()
        } catch (e: Exception) {
            log.error(
                "reconcile list desired failed",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
            return
        }
        for (desired in deployments) {
            try {
                tickOne(desired)
            } catch (e: Exception) {
                log.error(
                    "reconcile deployment failed",
                    "deployment_id" to desired.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
    }

    private fun safeTickAll() {
        try {
            telemetry.inSpan("reconcile.tick") {
                tickAll()
            }
        } catch (e: Exception) {
            log.error(
                "reconcile tick failed",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
        }
    }

    private fun tickOne(desired: DesiredState) {
        val started = System.currentTimeMillis()
        val deploymentId = UUID.fromString(desired.deploymentId)
        val previous = statusStore.findByDeploymentId(deploymentId)

        val (actual, plan, healthy) = try {
            val loaded = runtimeClient.loadActual(deploymentId)
            val computed = computePlan(desired, loaded)
            Triple(loaded, computed, true)
        } catch (e: RuntimeUnreachableException) {
            log.warn(
                "reconcile runtime unreachable",
                "deployment_id" to desired.deploymentId,
                "error" to (e.message ?: e.javaClass.simpleName),
            )
            val keptActual = previous?.actual ?: ActualState()
            val keptPlan = previous?.plan ?: ReconcilePlan.EMPTY
            Triple(keptActual, keptPlan, false)
        }

        val snapshot = ReconcileSnapshot(
            deploymentId = deploymentId,
            lastRunAt = Instant.now(clock),
            desired = desired,
            actual = actual,
            plan = plan,
            controllerHealthy = healthy,
        )
        statusStore.upsert(snapshot)

        val durationMs = System.currentTimeMillis() - started
        log.info(
            "reconcile tick",
            "deployment_id" to desired.deploymentId,
            "desired_replicas" to desired.replicas,
            "actual_replicas" to actual.replicas.size,
            "plan_size" to plan.size,
            "tick_duration_ms" to durationMs,
            "controller_healthy" to healthy,
        )
        telemetry.recordReconcileTick(plan.size, healthy)
    }
}
