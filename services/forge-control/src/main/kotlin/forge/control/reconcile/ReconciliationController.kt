package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.scheduler.DisruptionBudgetGuard
import forge.control.scheduler.PlacementService
import forge.control.scheduler.StaleReplicaFencer
import forge.control.telemetry.Telemetry
import java.time.Clock
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Interval reconciliation loop. Loads desired + actual, computes a plan
 * (single-version, rolling, or rollback), executes start/wait/shift/drain/stop
 * actions, re-observes, and persists a status snapshot.
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
    private val lastHealthyStore: LastHealthyStore = InMemoryLastHealthyStore(),
    private val rolloutTimer: RolloutTimer = RolloutTimer(clock),
    private val healthEvaluator: HealthEvaluator = HealthEvaluator(),
    private val rollbacker: Rollbacker = Rollbacker(),
    private val rollbackEnabled: Boolean = true,
    private val transitionRecorder: TransitionRecorder = StatusOnlyTransitionRecorder(deploymentStore),
    private val placementService: PlacementService? = null,
    private val staleReplicaFencer: StaleReplicaFencer? = null,
    private val secretsClient: SecretsClient = NoOpSecretsClient,
    private val injectMaskInLogs: Boolean = true,
    private val attachmentEnvSource: forge.control.manageddb.AttachmentEnvSource =
        forge.control.manageddb.NoOpAttachmentEnvSource,
    private val disruptionBudgetGuard: DisruptionBudgetGuard? = null,
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
        placementService = placementService,
        staleReplicaFencer = staleReplicaFencer,
        secretsClient = secretsClient,
        injectMaskInLogs = injectMaskInLogs,
        attachmentEnvSource = attachmentEnvSource,
        disruptionBudgetGuard = disruptionBudgetGuard,
    ),
) : AutoCloseable {
    private val running = AtomicBoolean(false)
    private val failedTargetImages = ConcurrentHashMap<String, String>()

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
            "rollback_enabled" to rollbackEnabled,
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
        restoreTimer(desired.deploymentId, previous)

        // Resolve fingerprint (and warm resolve path) for redeploy detection.
        // Values are not retained on DesiredState — only the fingerprint hash.
        val desiredWithSecrets = attachSecretsFingerprint(desired)

        var healthy: Boolean
        var actualBefore: ActualState
        try {
            actualBefore = runtimeClient.observe(deploymentId)
            healthy = true
        } catch (e: RuntimeUnreachableException) {
            log.warn(
                "reconcile runtime unreachable",
                    "deployment_id" to desiredWithSecrets.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
            )
            actualBefore = previous?.actual ?: ActualState()
            healthy = false
        }

        if (healthy) {
            // Drop reserved capacity for replica indices the planner will never Stop
            // (observation already lost them after a prior scale-up).
            placementService?.releaseOrphanedAboveDesired(
                deploymentId,
                desiredWithSecrets.replicas,
            )
            val fenced = reconciler.fenceStale(desiredWithSecrets, actualBefore)
            if (fenced.isNotEmpty()) {
                try {
                    actualBefore = runtimeClient.observe(deploymentId)
                } catch (e: RuntimeUnreachableException) {
                    log.warn(
                        "reconcile runtime unreachable after fence",
                        "deployment_id" to desiredWithSecrets.deploymentId,
                        "error" to (e.message ?: e.javaClass.simpleName),
                    )
                    healthy = false
                }
            }
        }

        val serviceKey = serviceKey(desiredWithSecrets)
        val lastHealthy = lastHealthyFor(desiredWithSecrets)
        var workingDesired = desiredWithSecrets
        var lifecycle = resolveLifecycle(deploymentId, previous)
        val rolling = healthy && needsRollingUpdate(workingDesired, actualBefore)
        val timedOut = rolloutTimer.isTimedOut(
            workingDesired.deploymentId,
            workingDesired.rollout.timeoutSeconds,
        )

        if (healthy && rolling && lifecycle != DeploymentLifecycle.RollingBack) {
            if (lifecycle != DeploymentLifecycle.Deploying) {
                lifecycle = recordTransition(
                    deploymentId = deploymentId,
                    from = lifecycle,
                    to = DeploymentLifecycle.Deploying,
                    image = workingDesired.image,
                    desiredReplicas = workingDesired.replicas,
                    actualReplicas = actualBefore.replicas.size,
                    reason = "rollout started",
                )
            }
            rolloutTimer.start(workingDesired.deploymentId)
        }

        val health = if (healthy && lifecycle == DeploymentLifecycle.Deploying) {
            healthEvaluator.evaluate(workingDesired, actualBefore, timedOut)
        } else {
            null
        }

        // Prefer success when readiness completes in the same tick as timeout.
        if (health == RolloutHealth.Success) {
            lifecycle = markDeployed(workingDesired, deploymentId, serviceKey, lastHealthy)
            rolloutTimer.clear(workingDesired.deploymentId)
            failedTargetImages.remove(workingDesired.deploymentId)
        } else if (
            rollbackEnabled &&
            lastHealthy != null &&
            (
                lifecycle == DeploymentLifecycle.RollingBack ||
                    health == RolloutHealth.Failed
                )
        ) {
            if (lifecycle != DeploymentLifecycle.RollingBack) {
                if (workingDesired.image != lastHealthy.image) {
                    failedTargetImages.putIfAbsent(workingDesired.deploymentId, workingDesired.image)
                }
                val reason = if (timedOut) "rollout timeout" else "unhealthy target replica"
                lifecycle = recordTransition(
                    deploymentId = deploymentId,
                    from = lifecycle,
                    to = DeploymentLifecycle.RollingBack,
                    image = workingDesired.image,
                    desiredReplicas = workingDesired.replicas,
                    actualReplicas = actualBefore.replicas.size,
                    reason = reason,
                )
            }
            workingDesired = beginOrContinueRollback(workingDesired, deploymentId, lastHealthy)
        } else if (health == RolloutHealth.Failed && lastHealthy == null) {
            lifecycle = recordTransition(
                deploymentId = deploymentId,
                from = lifecycle,
                to = DeploymentLifecycle.Failed,
                image = workingDesired.image,
                desiredReplicas = workingDesired.replicas,
                actualReplicas = actualBefore.replicas.size,
                reason = "rollout failed without last healthy",
            )
            rolloutTimer.clear(workingDesired.deploymentId)
        }

        val plan = when {
            !healthy -> previous?.plan ?: ReconcilePlan.EMPTY
            lifecycle == DeploymentLifecycle.RollingBack && lastHealthy != null -> {
                val failedImage = failedTargetImages[workingDesired.deploymentId]
                rollbacker.planRollback(
                    desired = workingDesired,
                    actual = actualBefore,
                    lastHealthy = lastHealthy,
                    failedTargetImage = failedImage,
                )
            }
            else -> computeReconcilePlan(workingDesired, actualBefore)
        }

        var executed = emptyList<ExecutedAction>()
        if (healthy && plan.actions.isNotEmpty()) {
            try {
                val spanName = when {
                    lifecycle == DeploymentLifecycle.RollingBack -> "reconcile.rollback"
                    needsRollingUpdate(workingDesired, actualBefore) -> "reconcile.rolling_update"
                    else -> "reconcile.execute"
                }
                val executeDesired = if (lifecycle == DeploymentLifecycle.RollingBack && lastHealthy != null) {
                    workingDesired.copy(image = lastHealthy.image, replicas = maxOf(workingDesired.replicas, lastHealthy.replicas))
                } else {
                    workingDesired
                }
                executed = telemetry.inSpan(spanName) {
                    reconciler.execute(executeDesired, actualBefore, plan)
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
                val recomputed = when {
                    lifecycle == DeploymentLifecycle.RollingBack && lastHealthy != null ->
                        rollbacker.planRollback(
                            desired = workingDesired,
                            actual = reloaded,
                            lastHealthy = lastHealthy,
                            failedTargetImage = failedTargetImages[workingDesired.deploymentId],
                        )
                    else -> computeReconcilePlan(workingDesired, reloaded)
                }
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

        if (healthyAfter && lifecycle == DeploymentLifecycle.RollingBack && lastHealthy != null) {
            if (rollbacker.isRestored(actualAfter, lastHealthy.copy(replicas = maxOf(lastHealthy.replicas, workingDesired.replicas)))) {
                val elapsedMs = rolloutTimer.elapsed(workingDesired.deploymentId).toMillis()
                    .takeIf { it > 0 } ?: (System.currentTimeMillis() - started)
                val failedImage = failedTargetImages[workingDesired.deploymentId] ?: ""
                lifecycle = recordTransition(
                    deploymentId = deploymentId,
                    from = DeploymentLifecycle.RollingBack,
                    to = DeploymentLifecycle.RolledBack,
                    image = lastHealthy.image,
                    desiredReplicas = maxOf(lastHealthy.replicas, workingDesired.replicas),
                    actualReplicas = actualAfter.replicas.size,
                    reason = "restored last healthy image=$failedImage→${lastHealthy.image}",
                )
                log.info(
                    "rollout outcome",
                    "deployment_id" to workingDesired.deploymentId,
                    "outcome" to "rolled_back",
                    "elapsed_ms" to elapsedMs,
                    "target_image" to failedImage,
                    "restored_image" to lastHealthy.image,
                )
                telemetry.recordRolloutResult("rolled_back")
                telemetry.recordRollbackDuration(elapsedMs)
                rolloutTimer.clear(workingDesired.deploymentId)
                failedTargetImages.remove(workingDesired.deploymentId)
            }
        } else if (healthyAfter && lifecycle == DeploymentLifecycle.Deploying) {
            val afterHealth = healthEvaluator.evaluate(
                workingDesired,
                actualAfter,
                rolloutTimer.isTimedOut(workingDesired.deploymentId, workingDesired.rollout.timeoutSeconds),
            )
            if (afterHealth == RolloutHealth.Success) {
                lifecycle = markDeployed(workingDesired, deploymentId, serviceKey, lastHealthy)
                rolloutTimer.clear(workingDesired.deploymentId)
                failedTargetImages.remove(workingDesired.deploymentId)
            }
        } else if (
            healthyAfter &&
            lifecycle == DeploymentLifecycle.Pending &&
            !needsRollingUpdate(workingDesired, actualAfter) &&
            healthEvaluator.evaluate(workingDesired, actualAfter, timedOut = false) == RolloutHealth.Success
        ) {
            // Initial converge / already-healthy single version.
            lifecycle = markDeployed(workingDesired, deploymentId, serviceKey, lastHealthy)
        }

        val degraded = executed.any {
            it.action == ReconcileAction.WaitReady.name &&
                it.result == ActionResult.Held &&
                it.detail == "readiness_timeout"
        }
        val finalPlan = when {
            lifecycle == DeploymentLifecycle.RollingBack || lifecycle == DeploymentLifecycle.RolledBack ->
                planAfter.copy(phase = RolloutPhase.Degraded.wire())
            degraded -> planAfter.copy(phase = RolloutPhase.Degraded.wire())
            else -> planAfter
        }

        val readyCount = actualAfter.replicas.count {
            it.statusEnum() == ReplicaStatus.Ready
        }
        telemetry.recordReplicasReady(readyCount)

        if (finalPlan.phaseEnum() == RolloutPhase.Rolling) {
            log.info(
                "reconcile rolling",
                "deployment_id" to workingDesired.deploymentId,
                "from_image" to (finalPlan.currentImage ?: ""),
                "to_image" to (finalPlan.targetImage ?: workingDesired.image),
                "updated_replicas" to finalPlan.updatedReplicas,
                "total_replicas" to finalPlan.totalReplicas,
                "phase" to finalPlan.phase,
            )
        }

        val lastHealthyImage = lastHealthyFor(workingDesired)?.image
            ?: previous?.lastHealthyImage
        val snapshot = ReconcileSnapshot(
            deploymentId = deploymentId,
            lastRunAt = Instant.now(clock),
            desired = workingDesired,
            actual = actualAfter,
            plan = finalPlan,
            controllerHealthy = healthyAfter,
            deploymentStatus = lifecycle.wire(),
            lastHealthyImage = lastHealthyImage,
            rolloutStartedAt = rolloutTimer.startedAt(workingDesired.deploymentId),
        )
        statusStore.upsert(snapshot)

        val durationMs = System.currentTimeMillis() - started
        log.info(
            "reconcile tick",
            "deployment_id" to workingDesired.deploymentId,
            "desired_replicas" to workingDesired.replicas,
            "actual_replicas" to actualAfter.replicas.size,
            "plan_size" to finalPlan.size,
            "phase" to finalPlan.phase,
            "status" to lifecycle.wire(),
            "updated_replicas" to finalPlan.updatedReplicas,
            "tick_duration_ms" to durationMs,
            "controller_healthy" to healthyAfter,
        )
        telemetry.recordReconcileTick(finalPlan.size, healthyAfter)
    }

    private fun beginOrContinueRollback(
        desired: DesiredState,
        deploymentId: UUID,
        lastHealthy: LastHealthyDeployment,
    ): DesiredState {
        if (desired.image != lastHealthy.image) {
            failedTargetImages.putIfAbsent(desired.deploymentId, desired.image)
            try {
                deploymentStore.setDesiredImage(deploymentId, lastHealthy.image)
            } catch (e: Exception) {
                log.warn(
                    "reconcile restore desired image failed",
                    "deployment_id" to desired.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
        return desired.copy(
            image = lastHealthy.image,
            replicas = maxOf(desired.replicas, lastHealthy.replicas),
        )
    }

    private fun markDeployed(
        desired: DesiredState,
        deploymentId: UUID,
        serviceKey: UUID?,
        previousHealthy: LastHealthyDeployment?,
    ): DeploymentLifecycle {
        val from = resolveLifecycle(deploymentId, null)
        recordTransition(
            deploymentId = deploymentId,
            from = from,
            to = DeploymentLifecycle.Deployed,
            image = desired.image,
            desiredReplicas = desired.replicas,
            actualReplicas = null,
            reason = "all replicas ready",
        )
        if (serviceKey != null) {
            lastHealthyStore.put(
                LastHealthyDeployment(
                    serviceId = serviceKey,
                    deploymentId = deploymentId,
                    image = desired.image,
                    replicas = desired.replicas,
                ),
            )
        }
        val elapsedMs = rolloutTimer.elapsed(desired.deploymentId).toMillis()
        log.info(
            "rollout outcome",
            "deployment_id" to desired.deploymentId,
            "outcome" to "deployed",
            "elapsed_ms" to elapsedMs,
            "target_image" to desired.image,
            "restored_image" to (previousHealthy?.image ?: desired.image),
        )
        telemetry.recordRolloutResult("deployed")
        return DeploymentLifecycle.Deployed
    }

    private fun resolveLifecycle(
        deploymentId: UUID,
        previous: ReconcileSnapshot?,
    ): DeploymentLifecycle {
        val fromStore = deploymentStore.getStatus(deploymentId)
        if (fromStore != null && fromStore in DeploymentLifecycle.WIRES) {
            return DeploymentLifecycle.parse(fromStore)
        }
        return DeploymentLifecycle.parse(previous?.deploymentStatus)
    }

    private fun recordTransition(
        deploymentId: UUID,
        from: DeploymentLifecycle,
        to: DeploymentLifecycle,
        image: String?,
        desiredReplicas: Int?,
        actualReplicas: Int?,
        reason: String,
    ): DeploymentLifecycle {
        try {
            transitionRecorder.transition(
                deploymentId = deploymentId,
                to = to,
                from = from,
                image = image,
                desiredReplicas = desiredReplicas,
                actualReplicas = actualReplicas,
                reason = reason,
            )
        } catch (e: Exception) {
            log.warn(
                "reconcile persist status failed",
                "deployment_id" to deploymentId.toString(),
                "status" to to.wire(),
                "error" to (e.message ?: e.javaClass.simpleName),
            )
            // Best-effort fallback so reconcile can continue if history write fails transiently.
            try {
                deploymentStore.setStatus(deploymentId, to.wire())
            } catch (_: Exception) {
                // already logged above
            }
        }
        return to
    }

    private fun lastHealthyFor(desired: DesiredState): LastHealthyDeployment? {
        val key = serviceKey(desired) ?: return null
        return lastHealthyStore.get(key)
    }

    private fun serviceKey(desired: DesiredState): UUID? =
        desired.serviceId.takeIf { it.isNotBlank() }?.let { runCatching { UUID.fromString(it) }.getOrNull() }

    private fun restoreTimer(deploymentId: String, previous: ReconcileSnapshot?) {
        val started = previous?.rolloutStartedAt ?: return
        if (rolloutTimer.startedAt(deploymentId) == null) {
            rolloutTimer.markStarted(deploymentId, started)
        }
    }

    /**
     * Ask Secrets for the current version fingerprint (env values discarded).
     * Missing/unavailable Secrets does not block the tick — StartReplica holds instead.
     */
    private fun attachSecretsFingerprint(desired: DesiredState): DesiredState {
        if (desired.projectId.isBlank() || desired.environmentName.isBlank()) {
            return desired
        }
        return when (
            val result = secretsClient.resolve(
                projectId = desired.projectId,
                environment = desired.environmentName,
                service = desired.serviceSlug,
            )
        ) {
            is SecretsResolveResult.Ok ->
                desired.copy(secretsFingerprint = result.bundle.versionFingerprint)
            else -> desired
        }
    }
}
