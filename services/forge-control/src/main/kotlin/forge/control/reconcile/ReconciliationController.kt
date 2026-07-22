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
 * Interval reconciliation loop. Loads desired + actual, computes a plan
 * (single-version or rolling), executes start/wait/shift/drain/stop actions,
 * re-observes, and persists a status snapshot.
 */
class ReconciliationController(
    private val deploymentStore: DeploymentStore,
    private val runtimeClient: RuntimeClient,
    private val statusStore: ReconcileStatusStore,
    private val log: JsonLog,
    private val intervalMs: Long,
    private val enabled: Boolean,
    private val maxActionsPerTick: Int = 5,
    private val clock: Clock = Clock.systemUTC(),
    private val telemetry: Telemetry = Telemetry.current(),
    private val readinessGate: ReadinessGate = ReadinessGate(runtimeClient),
    private val trafficShifter: TrafficShifter = TrafficShifter(NoOpGatewayClient()),
    private val readinessMaxWaitSeconds: Long = 60,
    private val scheduler: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "forge-reconcile").apply { isDaemon = true }
    },
    private val reconciler: Reconciler = Reconciler(
        runtimeClient = runtimeClient,
        log = log,
        maxActionsPerTick = maxActionsPerTick,
        telemetry = telemetry,
        readinessGate = readinessGate,
        trafficShifter = trafficShifter,
        readinessMaxWaitSeconds = readinessMaxWaitSeconds,
    ),
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
            "max_actions_per_tick" to maxActionsPerTick,
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

        val (actualBefore, plan, healthy) = try {
            val loaded = runtimeClient.observe(deploymentId)
            val computed = computeReconcilePlan(desired, loaded)
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

        var executed = emptyList<ExecutedAction>()
        if (healthy && plan.actions.isNotEmpty()) {
            try {
                val spanName = if (needsRollingUpdate(desired, actualBefore)) {
                    "reconcile.rolling_update"
                } else {
                    "reconcile.execute"
                }
                executed = telemetry.inSpan(spanName) {
                    reconciler.execute(desired, actualBefore, plan)
                }
            } catch (e: Exception) {
                log.error(
                    "reconcile execute failed",
                    "deployment_id" to desired.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }

        val (actualAfter, planAfter, healthyAfter) = if (!healthy) {
            Triple(actualBefore, plan, false)
        } else {
            try {
                val reloaded = runtimeClient.observe(deploymentId)
                val recomputed = computeReconcilePlan(desired, reloaded)
                Triple(reloaded, recomputed, true)
            } catch (e: RuntimeUnreachableException) {
                log.warn(
                    "reconcile runtime unreachable after execute",
                    "deployment_id" to desired.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
                Triple(actualBefore, plan, false)
            }
        }

        val degraded = executed.any {
            it.action == ReconcileAction.WaitReady.name &&
                it.result == ActionResult.Held &&
                it.detail == "readiness_timeout"
        }
        val finalPlan = if (degraded) {
            planAfter.copy(phase = RolloutPhase.Degraded.wire())
        } else {
            planAfter
        }

        val readyCount = actualAfter.replicas.count {
            it.statusEnum() == ReplicaStatus.Ready
        }
        telemetry.recordReplicasReady(readyCount)

        if (finalPlan.phaseEnum() == RolloutPhase.Rolling) {
            log.info(
                "reconcile rolling",
                "deployment_id" to desired.deploymentId,
                "from_image" to (finalPlan.currentImage ?: ""),
                "to_image" to (finalPlan.targetImage ?: desired.image),
                "updated_replicas" to finalPlan.updatedReplicas,
                "total_replicas" to finalPlan.totalReplicas,
                "phase" to finalPlan.phase,
            )
        }

        val snapshot = ReconcileSnapshot(
            deploymentId = deploymentId,
            lastRunAt = Instant.now(clock),
            desired = desired,
            actual = actualAfter,
            plan = finalPlan,
            controllerHealthy = healthyAfter,
        )
        statusStore.upsert(snapshot)

        val durationMs = System.currentTimeMillis() - started
        log.info(
            "reconcile tick",
            "deployment_id" to desired.deploymentId,
            "desired_replicas" to desired.replicas,
            "actual_replicas" to actualAfter.replicas.size,
            "plan_size" to finalPlan.size,
            "phase" to finalPlan.phase,
            "updated_replicas" to finalPlan.updatedReplicas,
            "tick_duration_ms" to durationMs,
            "controller_healthy" to healthyAfter,
        )
        telemetry.recordReconcileTick(finalPlan.size, healthyAfter)
    }
}
