package forge.control.http

import forge.control.http.dto.ReconcileStatusResponse
import forge.control.http.dto.toResponse
import forge.control.http.dto.toView
import forge.control.reconcile.ActualState
import forge.control.reconcile.DeploymentStore
import forge.control.reconcile.ReconcilePlan
import forge.control.reconcile.ReconcileStatusStore
import forge.control.reconcile.RuntimeClient
import forge.control.reconcile.RuntimeUnreachableException
import forge.control.reconcile.computeReconcilePlan
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get

fun Route.reconcileStatusRoutes(
    deploymentStore: DeploymentStore,
    runtimeClient: RuntimeClient,
    statusStore: ReconcileStatusStore,
) {
    get("/v1/deployments/{deploymentId}/reconcile") {
        val deploymentId = call.parameters.requireUuid("deploymentId")
        val desired = deploymentStore.findDesired(deploymentId)
            ?: throw ApiException.NotFound(
                "deployment not found",
                mapOf("id" to deploymentId.toString()),
            )

        val snapshot = statusStore.findByDeploymentId(deploymentId)
        if (snapshot != null) {
            call.respond(snapshot.toResponse())
            return@get
        }

        // No controller tick yet — compute a live view without persisting.
        val (actual, plan, healthy) = try {
            val loaded = runtimeClient.loadActual(deploymentId)
            Triple(loaded, computeReconcilePlan(desired, loaded), true)
        } catch (_: RuntimeUnreachableException) {
            Triple(ActualState(), ReconcilePlan.EMPTY, false)
        }
        val updated = plan.updatedReplicas
        val total = plan.totalReplicas.takeIf { it > 0 } ?: desired.replicas
        call.respond(
            ReconcileStatusResponse(
                deploymentId = deploymentId.toString(),
                desired = desired.toView(),
                actual = actual.toView(),
                plan = plan.toView(),
                lastRunAt = null,
                controllerHealthy = healthy,
                phase = plan.phase,
                updatedReplicas = "$updated/$total",
                currentImage = plan.currentImage,
                targetImage = plan.targetImage,
            ),
        )
    }
}
