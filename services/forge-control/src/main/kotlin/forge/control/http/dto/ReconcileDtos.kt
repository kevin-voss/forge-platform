package forge.control.http.dto

import forge.control.reconcile.ActualState
import forge.control.reconcile.DesiredState
import forge.control.reconcile.ReconcileActionItem
import forge.control.reconcile.ReconcilePlan
import forge.control.reconcile.ReconcileSnapshot
import forge.control.reconcile.RolloutPolicy
import kotlinx.serialization.Serializable

@Serializable
data class ReconcileStatusResponse(
    val deploymentId: String,
    val desired: DesiredView,
    val actual: ActualView,
    val plan: List<PlanActionView>,
    val lastRunAt: String? = null,
    val controllerHealthy: Boolean,
)

@Serializable
data class DesiredView(
    val image: String,
    val replicas: Int,
    val rollout: RolloutView,
)

@Serializable
data class RolloutView(
    val batchSize: Int,
    val timeoutSeconds: Int,
)

@Serializable
data class ActualView(
    val replicas: List<ReplicaView>,
)

@Serializable
data class ReplicaView(
    val replicaId: String,
    val status: String,
)

@Serializable
data class PlanActionView(
    val action: String,
    val reason: String,
    val replicaId: String? = null,
)

fun DesiredState.toView(): DesiredView =
    DesiredView(
        image = image,
        replicas = replicas,
        rollout = rollout.toView(),
    )

fun RolloutPolicy.toView(): RolloutView =
    RolloutView(batchSize = batchSize, timeoutSeconds = timeoutSeconds)

fun ActualState.toView(): ActualView =
    ActualView(
        replicas = replicas.map { ReplicaView(replicaId = it.replicaId, status = it.status) },
    )

fun ReconcilePlan.toView(): List<PlanActionView> =
    actions.map { it.toView() }

fun ReconcileActionItem.toView(): PlanActionView =
    PlanActionView(action = action, reason = reason, replicaId = replicaId)

fun ReconcileSnapshot.toResponse(): ReconcileStatusResponse =
    ReconcileStatusResponse(
        deploymentId = deploymentId.toString(),
        desired = desired.toView(),
        actual = actual.toView(),
        plan = plan.toView(),
        lastRunAt = lastRunAt.toString(),
        controllerHealthy = controllerHealthy,
    )
