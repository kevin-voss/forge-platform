package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.util.UUID

enum class ActionResult {
    Created,
    Adopted,
    Recreated,
    Stopped,
    Skipped,
    Failed,
}

data class ExecutedAction(
    val action: String,
    val replicaIndex: Int?,
    val result: ActionResult,
    val durationMs: Long,
    val detail: String? = null,
)

/**
 * Applies a [ReconcilePlan] via Runtime, idempotently.
 * Bounds work per tick with [maxActionsPerTick].
 */
class Reconciler(
    private val runtimeClient: RuntimeClient,
    private val log: JsonLog,
    private val maxActionsPerTick: Int = 5,
    private val telemetry: Telemetry = Telemetry.current(),
) {
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
        }
        return results
    }

    private fun executeStart(
        desired: DesiredState,
        actual: ActualState,
        item: ReconcileActionItem,
    ): ExecutedAction {
        val index = resolveStartIndex(desired, actual, item)
        val deploymentId = UUID.fromString(desired.deploymentId)
        return telemetry.inSpan("reconcile.start_replica") {
            val outcome = runtimeClient.ensureWorkload(
                WorkloadEnsureRequest(
                    deploymentId = deploymentId,
                    serviceSlug = desired.serviceSlug,
                    serviceId = desired.serviceId,
                    replicaIndex = index,
                    image = desired.image,
                    port = desired.port,
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
            ExecutedAction(
                action = ReconcileAction.StopReplica.name,
                replicaIndex = index,
                result = ActionResult.Stopped,
                durationMs = 0,
            )
        }
    }

    private fun resolveStartIndex(
        desired: DesiredState,
        actual: ActualState,
        item: ReconcileActionItem,
    ): Int {
        item.replicaId?.toIntOrNull()?.let { return it }
        WorkloadNamer.parseReplicaIndex(item.replicaId)?.let { return it }

        // Prefer crashed slots still within the desired range.
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
            ActionResult.Skipped, ActionResult.Failed -> return
        }
        telemetry.recordReconcileAction(metricAction)
    }

    companion object {
        private val SATISFYING = setOf(
            ReplicaStatus.Pending,
            ReplicaStatus.Running,
            ReplicaStatus.Ready,
        )
    }
}
