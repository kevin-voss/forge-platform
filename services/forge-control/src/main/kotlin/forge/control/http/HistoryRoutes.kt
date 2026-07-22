package forge.control.http

import forge.control.http.dto.DeploymentEventView
import forge.control.http.dto.DeploymentHistoryResponse
import forge.control.reconcile.DeploymentHistory
import forge.control.reconcile.DeploymentStore
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import java.time.format.DateTimeFormatter

fun Route.historyRoutes(
    deploymentStore: DeploymentStore,
    history: DeploymentHistory,
) {
    get("/v1/deployments/{deploymentId}/history") {
        val deploymentId = call.parameters.requireUuid("deploymentId")
        deploymentStore.findDesired(deploymentId)
            ?: throw ApiException.NotFound(
                "deployment not found",
                mapOf("id" to deploymentId.toString()),
            )

        val events = history.listByDeploymentId(deploymentId).map { event ->
            DeploymentEventView(
                at = DateTimeFormatter.ISO_INSTANT.format(event.at),
                from = event.fromStatus,
                to = event.toStatus,
                image = event.image,
                desiredReplicas = event.desiredReplicas,
                actualReplicas = event.actualReplicas,
                reason = event.reason,
            )
        }
        call.respond(
            DeploymentHistoryResponse(
                deploymentId = deploymentId.toString(),
                events = events,
            ),
        )
    }
}
