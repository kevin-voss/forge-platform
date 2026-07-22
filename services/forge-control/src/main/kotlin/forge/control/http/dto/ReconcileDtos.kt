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
    val phase: String = "idle",
    val updatedReplicas: String? = null,
    val currentImage: String? = null,
    val targetImage: String? = null,
    val status: String = "pending",
    val lastHealthyImage: String? = null,
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
    val image: String? = null,
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
        replicas = replicas.map {
            ReplicaView(replicaId = it.replicaId, status = it.status, image = it.image)
        },
    )

fun ReconcilePlan.toView(): List<PlanActionView> =
    actions.map { it.toView() }

fun ReconcileActionItem.toView(): PlanActionView =
    PlanActionView(action = action, reason = reason, replicaId = replicaId)

fun ReconcileSnapshot.toResponse(): ReconcileStatusResponse {
    val updated = plan.updatedReplicas
    val total = plan.totalReplicas.takeIf { it > 0 } ?: desired.replicas
    return ReconcileStatusResponse(
        deploymentId = deploymentId.toString(),
        desired = desired.toView(),
        actual = actual.toView(),
        plan = plan.toView(),
        lastRunAt = lastRunAt.toString(),
        controllerHealthy = controllerHealthy,
        phase = plan.phase,
        updatedReplicas = "$updated/$total",
        currentImage = plan.currentImage,
        targetImage = plan.targetImage,
        status = deploymentStatus,
        lastHealthyImage = lastHealthyImage,
    )
}
