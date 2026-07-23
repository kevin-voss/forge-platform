package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.manageddb.AttachmentEnvResult
import forge.control.manageddb.AttachmentEnvSource
import forge.control.manageddb.NoOpAttachmentEnvSource
import forge.control.scheduler.PlaceResult
import forge.control.scheduler.PlacementService
import forge.control.scheduler.StaleReplicaFencer
import forge.control.telemetry.Telemetry
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

enum class ActionResult {
    Created,
    Adopted,
    Recreated,
    Stopped,
    Ready,
    Shifted,
    Drained,
    Skipped,
    Failed,
    Held,
}

data class ExecutedAction(
    val action: String,
    val replicaIndex: Int?,
    val result: ActionResult,
    val durationMs: Long,
    val detail: String? = null,
)

/**
 * Applies a [ReconcilePlan] via Runtime (+ Gateway for rolling), idempotently.
 * Bounds work per tick with [maxActionsPerTick].
 * Rolling steps stop the chain when WaitReady is not yet satisfied or traffic
 * shift/drain fails (fail-closed — do not stop old replicas).
 *
 * Lifecycle status transitions (deploying/deployed/…) are recorded by
 * [TransitionRecorder] in [ReconciliationController] — not here — so create/
 * ready/shift/stop actions stay idempotent under controller restart
 * (`EnsureOutcome.Adopted` when a labeled workload already exists).
 */
class Reconciler(
    private val runtimeClient: RuntimeClient,
    private val log: JsonLog,
    private val maxActionsPerTick: Int = 5,
    private val telemetry: Telemetry = Telemetry.current(),
    private val readinessGate: ReadinessGate = ReadinessGate(runtimeClient),
    private val trafficShifter: TrafficShifter = TrafficShifter(NoOpGatewayClient()),
    private val readinessMaxWaitSeconds: Long = 60,
    private val placementService: PlacementService? = null,
    private val staleReplicaFencer: StaleReplicaFencer? = null,
    private val secretsClient: SecretsClient = NoOpSecretsClient,
    private val injectMaskInLogs: Boolean = true,
    private val attachmentEnvSource: AttachmentEnvSource = NoOpAttachmentEnvSource,
) {
    private val waitStartedAt = ConcurrentHashMap<String, Long>()

    /**
     * Fence surplus replicas (e.g. recovered node still hosting rescheduled
     * workloads) before applying the plan. Returns fenced replica indices.
     */
    fun fenceStale(desired: DesiredState, actual: ActualState): List<Int> =
        staleReplicaFencer?.fence(desired, actual).orEmpty()

    fun execute(desired: DesiredState, actual: ActualState, plan: ReconcilePlan): List<ExecutedAction> {
        val deploymentId = UUID.fromString(desired.deploymentId)
        val results = mutableListOf<ExecutedAction>()
        val bounded = plan.actions.take(maxActionsPerTick.coerceAtLeast(0))

        for (item in bounded) {
            val started = System.currentTimeMillis()
            val executed = try {
                when (item.action) {
                    ReconcileAction.StartReplica.name ->
                        executeStart(desired, actual, item)
                    ReconcileAction.StopReplica.name ->
                        executeStop(desired, item)
                    ReconcileAction.WaitReady.name ->
                        executeWaitReady(desired, item)
                    ReconcileAction.ShiftTraffic.name ->
                        executeShift(item)
                    ReconcileAction.DrainReplica.name ->
                        executeDrain(desired, item)
                    ReconcileAction.NoOp.name ->
                        ExecutedAction(
                            action = item.action,
                            replicaIndex = null,
                            result = ActionResult.Skipped,
                            durationMs = 0,
                        )
                    else ->
                        ExecutedAction(
                            action = item.action,
                            replicaIndex = null,
                            result = ActionResult.Skipped,
                            durationMs = 0,
                            detail = "unknown action",
                        )
                }
            } catch (e: Exception) {
                ExecutedAction(
                    action = item.action,
                    replicaIndex = item.replicaId?.toIntOrNull(),
                    result = ActionResult.Failed,
                    durationMs = System.currentTimeMillis() - started,
                    detail = e.message ?: e.javaClass.simpleName,
                )
            }
            results += executed.copy(durationMs = System.currentTimeMillis() - started)
            logExecuted(deploymentId, executed)
            recordMetrics(executed)

            // Fail-closed / hold: do not continue to drain/stop after a hold or shift failure.
            if (shouldHaltPlan(executed)) break
        }
        return results
    }

    /** True when a WaitReady held past max wait (rollout should surface degraded). */
    fun isWaitTimedOut(desired: DesiredState, replicaIndex: Int): Boolean {
        val key = waitKey(desired.deploymentId, replicaIndex)
        val started = waitStartedAt[key] ?: return false
        return System.currentTimeMillis() - started >= readinessMaxWaitSeconds * 1_000
    }

    fun clearWait(deploymentId: String, replicaIndex: Int) {
        waitStartedAt.remove(waitKey(deploymentId, replicaIndex))
    }

    private fun shouldHaltPlan(executed: ExecutedAction): Boolean =
        when (executed.result) {
            ActionResult.Held -> when (executed.action) {
                ReconcileAction.StartReplica.name ->
                    // Hold deploy when required secrets cannot be resolved (fail-safe).
                    executed.detail?.startsWith("missing_secrets") == true ||
                        executed.detail?.startsWith("secrets_") == true
                ReconcileAction.WaitReady.name,
                ReconcileAction.ShiftTraffic.name,
                ReconcileAction.DrainReplica.name,
                -> true
                else -> false
            }
            ActionResult.Failed ->
                executed.action in setOf(
                    ReconcileAction.WaitReady.name,
                    ReconcileAction.ShiftTraffic.name,
                    ReconcileAction.DrainReplica.name,
                )
            else -> false
        }

    private fun executeStart(
        desired: DesiredState,
        actual: ActualState,
        item: ReconcileActionItem,
    ): ExecutedAction {
        val index = resolveStartIndex(desired, actual, item)
        val deploymentId = UUID.fromString(desired.deploymentId)
        return telemetry.inSpan("reconcile.start_replica") {
            when (val place = placeReplica(deploymentId, index, desired.serviceId)) {
                is PlaceResult.NoNode ->
                    return@inSpan ExecutedAction(
                        action = ReconcileAction.StartReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = "no_node_available",
                    )
                is PlaceResult.QueueFull ->
                    return@inSpan ExecutedAction(
                        action = ReconcileAction.StartReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = "queue_full",
                    )
                is PlaceResult.Pending ->
                    return@inSpan ExecutedAction(
                        action = ReconcileAction.StartReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Held,
                        durationMs = 0,
                        detail = "pending_placement",
                    )
                is PlaceResult.Ok, null -> Unit
            }

            val inject = resolveInjectEnv(desired)
            if (inject is InjectOutcome.Hold) {
                return@inSpan ExecutedAction(
                    action = ReconcileAction.StartReplica.name,
                    replicaIndex = index,
                    result = ActionResult.Held,
                    durationMs = 0,
                    detail = inject.detail,
                )
            }
            val bundle = (inject as InjectOutcome.Ready).bundle

            telemetry.inSpan("deploy.inject") {
                // Log keys + fingerprint only — never values.
                log.info(
                    "deploy.inject",
                    "deployment_id" to desired.deploymentId,
                    "service" to desired.serviceSlug,
                    "keys" to bundle.env.keys.sorted().joinToString(","),
                    "version_fingerprint" to bundle.versionFingerprint,
                    "masked" to injectMaskInLogs,
                )
            }

            val outcome = runtimeClient.ensureWorkload(
                WorkloadEnsureRequest(
                    deploymentId = deploymentId,
                    serviceSlug = desired.serviceSlug,
                    serviceId = desired.serviceId,
                    replicaIndex = index,
                    image = desired.image,
                    port = desired.port,
                    environment = bundle.env,
                    secretsFingerprint = bundle.versionFingerprint,
                ),
            )
            val result = when (outcome) {
                EnsureOutcome.Created -> ActionResult.Created
                EnsureOutcome.Adopted -> ActionResult.Adopted
                EnsureOutcome.Recreated -> ActionResult.Recreated
            }
            ExecutedAction(
                action = ReconcileAction.StartReplica.name,
                replicaIndex = index,
                result = result,
                durationMs = 0,
                detail = outcome.name.lowercase(),
            )
        }
    }

    private sealed class InjectOutcome {
        data class Ready(val bundle: ResolvedEnvBundle) : InjectOutcome()
        data class Hold(val detail: String) : InjectOutcome()
    }

    /**
     * Fetch resolved secrets/config for the service and merge managed-DB
     * attachment URLs. Holds deploy on missing required bindings, attachment
     * secret failure, or Secrets unavailability. Never persists plaintext.
     */
    private fun resolveInjectEnv(desired: DesiredState): InjectOutcome {
        val base = if (desired.projectId.isBlank() || desired.environmentName.isBlank()) {
            SecretsResolveResult.Ok(
                ResolvedEnvBundle(env = emptyMap(), versionFingerprint = desired.secretsFingerprint),
            )
        } else {
            secretsClient.resolve(
                projectId = desired.projectId,
                environment = desired.environmentName,
                service = desired.serviceSlug,
            )
        }
        val baseBundle = when (base) {
            is SecretsResolveResult.Ok -> base.bundle
            is SecretsResolveResult.Missing ->
                return InjectOutcome.Hold("missing_secrets:${base.detail}")
            is SecretsResolveResult.Unavailable ->
                return InjectOutcome.Hold("secrets_unavailable:${base.detail}")
            is SecretsResolveResult.Failed ->
                return InjectOutcome.Hold("secrets_resolve_failed:${base.detail}")
        }

        val attachment = attachmentEnvSource.resolveForApplication(desired.applicationId)
        return when (attachment) {
            is AttachmentEnvResult.Empty -> InjectOutcome.Ready(baseBundle)
            is AttachmentEnvResult.Hold ->
                InjectOutcome.Hold("managed_db_attach:${attachment.detail}")
            is AttachmentEnvResult.Ready -> {
                // Attachment env vars win so DATABASE_URL comes from the managed DB.
                val merged = baseBundle.env + attachment.env
                val fingerprint = listOf(baseBundle.versionFingerprint, attachment.fingerprint)
                    .filter { it.isNotBlank() }
                    .joinToString(":")
                InjectOutcome.Ready(
                    ResolvedEnvBundle(env = merged, versionFingerprint = fingerprint),
                )
            }
        }
    }

    private fun placeReplica(
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String,
    ): PlaceResult? {
        val service = placementService ?: return null
        return service.placeAndPersist(
            deploymentId = deploymentId,
            replicaIndex = replicaIndex,
            serviceId = serviceId.takeIf { it.isNotBlank() },
        )
    }

    private fun executeStop(
        desired: DesiredState,
        item: ReconcileActionItem,
    ): ExecutedAction {
        val index = item.replicaId?.toIntOrNull()
            ?: WorkloadNamer.parseReplicaIndex(item.replicaId)
            ?: throw IllegalArgumentException("StopReplica missing replica index")
        val deploymentId = UUID.fromString(desired.deploymentId)
        return telemetry.inSpan("reconcile.stop_replica") {
            val runtimeId = WorkloadNamer.runtimeDeploymentId(
                desired.serviceSlug,
                deploymentId,
                index,
            )
            runtimeClient.stopWorkload(runtimeId)
            placementService?.releasePlacement(deploymentId, index)
            clearWait(desired.deploymentId, index)
            ExecutedAction(
                action = ReconcileAction.StopReplica.name,
                replicaIndex = index,
                result = ActionResult.Stopped,
                durationMs = 0,
            )
        }
    }

    private fun executeWaitReady(
        desired: DesiredState,
        item: ReconcileActionItem,
    ): ExecutedAction {
        val index = item.replicaId?.toIntOrNull()
            ?: WorkloadNamer.parseReplicaIndex(item.replicaId)
            ?: throw IllegalArgumentException("WaitReady missing replica index")
        val deploymentId = UUID.fromString(desired.deploymentId)
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            desired.serviceSlug,
            deploymentId,
            index,
        )
        return telemetry.inSpan("reconcile.wait_ready") {
            val key = waitKey(desired.deploymentId, index)
            waitStartedAt.putIfAbsent(key, System.currentTimeMillis())
            val waitedMs = System.currentTimeMillis() - (waitStartedAt[key] ?: System.currentTimeMillis())
            val check = readinessGate.checkOnce(runtimeId)
            when (check.outcome) {
                ReadinessOutcome.Ready -> {
                    clearWait(desired.deploymentId, index)
                    ExecutedAction(
                        action = ReconcileAction.WaitReady.name,
                        replicaIndex = index,
                        result = ActionResult.Ready,
                        durationMs = waitedMs,
                        detail = "ready",
                    )
                }
                ReadinessOutcome.TimedOut, ReadinessOutcome.NotReady -> {
                    val timedOut = waitedMs >= readinessMaxWaitSeconds * 1_000
                    ExecutedAction(
                        action = ReconcileAction.WaitReady.name,
                        replicaIndex = index,
                        result = ActionResult.Held,
                        durationMs = waitedMs,
                        detail = if (timedOut) "readiness_timeout" else "not_ready",
                    )
                }
                ReadinessOutcome.Unreachable ->
                    ExecutedAction(
                        action = ReconcileAction.WaitReady.name,
                        replicaIndex = index,
                        result = ActionResult.Held,
                        durationMs = waitedMs,
                        detail = "runtime_unreachable",
                    )
            }
        }
    }

    private fun executeShift(item: ReconcileActionItem): ExecutedAction {
        val index = item.replicaId?.toIntOrNull()
            ?: WorkloadNamer.parseReplicaIndex(item.replicaId)
        return telemetry.inSpan("reconcile.shift_traffic") {
            val result = trafficShifter.shiftToReady(item.replicaId ?: index?.toString().orEmpty())
            when (result.outcome) {
                ShiftOutcome.Shifted ->
                    ExecutedAction(
                        action = ReconcileAction.ShiftTraffic.name,
                        replicaIndex = index,
                        result = ActionResult.Shifted,
                        durationMs = 0,
                        detail = result.detail,
                    )
                ShiftOutcome.GatewayUnreachable ->
                    ExecutedAction(
                        action = ReconcileAction.ShiftTraffic.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = "gateway_unreachable",
                    )
                else ->
                    ExecutedAction(
                        action = ReconcileAction.ShiftTraffic.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = result.detail,
                    )
            }
        }
    }

    private fun executeDrain(desired: DesiredState, item: ReconcileActionItem): ExecutedAction {
        val index = item.replicaId?.toIntOrNull()
            ?: WorkloadNamer.parseReplicaIndex(item.replicaId)
            ?: throw IllegalArgumentException("DrainReplica missing replica index")
        val deploymentId = UUID.fromString(desired.deploymentId)
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            desired.serviceSlug,
            deploymentId,
            index,
        )
        return telemetry.inSpan("reconcile.drain_replica") {
            // Mark Runtime status stopped first so Gateway sync Ready=false.
            runtimeClient.drainWorkload(runtimeId)
            val result = trafficShifter.drain(runtimeId)
            when (result.outcome) {
                ShiftOutcome.Drained, ShiftOutcome.Shifted ->
                    ExecutedAction(
                        action = ReconcileAction.DrainReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Drained,
                        durationMs = 0,
                        detail = result.detail,
                    )
                ShiftOutcome.GatewayUnreachable ->
                    ExecutedAction(
                        action = ReconcileAction.DrainReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = "gateway_unreachable",
                    )
                else ->
                    ExecutedAction(
                        action = ReconcileAction.DrainReplica.name,
                        replicaIndex = index,
                        result = ActionResult.Failed,
                        durationMs = 0,
                        detail = result.detail,
                    )
            }
        }
    }

    private fun resolveStartIndex(
        desired: DesiredState,
        actual: ActualState,
        item: ReconcileActionItem,
    ): Int {
        item.replicaId?.toIntOrNull()?.let { return it }
        WorkloadNamer.parseReplicaIndex(item.replicaId)?.let { return it }

        val crashed = CrashDetector.crashedReplicas(actual)
            .mapNotNull { it.resolvedIndex() }
            .filter { it in 0 until desired.replicas }
            .sorted()
        if (crashed.isNotEmpty()) return crashed.first()

        val used = actual.replicas
            .filter { it.statusEnum() in SATISFYING }
            .mapNotNull { it.resolvedIndex() }
            .toSet()
        var candidate = 0
        while (candidate in used) candidate++
        return candidate
    }

    private fun logExecuted(deploymentId: UUID, executed: ExecutedAction) {
        log.info(
            "reconcile action",
            "deployment_id" to deploymentId.toString(),
            "action" to executed.action,
            "replica_index" to (executed.replicaIndex ?: -1),
            "result" to executed.result.name.lowercase(),
            "duration_ms" to executed.durationMs,
            "detail" to (executed.detail ?: ""),
        )
    }

    private fun recordMetrics(executed: ExecutedAction) {
        val metricAction = when (executed.result) {
            ActionResult.Created, ActionResult.Adopted -> "start"
            ActionResult.Recreated -> "recreate"
            ActionResult.Stopped -> "stop"
            ActionResult.Ready -> "wait_ready"
            ActionResult.Shifted -> "shift"
            ActionResult.Drained -> "drain"
            ActionResult.Skipped, ActionResult.Failed, ActionResult.Held -> return
        }
        telemetry.recordReconcileAction(metricAction)
        if (executed.action in setOf(
                ReconcileAction.StartReplica.name,
                ReconcileAction.WaitReady.name,
                ReconcileAction.ShiftTraffic.name,
                ReconcileAction.DrainReplica.name,
                ReconcileAction.StopReplica.name,
            ) && executed.result != ActionResult.Held
        ) {
            telemetry.recordRolloutStep(executed.action)
        }
    }

    private fun waitKey(deploymentId: String, replicaIndex: Int): String =
        "$deploymentId:$replicaIndex"

    companion object {
        private val SATISFYING = setOf(
            ReplicaStatus.Pending,
            ReplicaStatus.Running,
            ReplicaStatus.Ready,
        )
    }
}
