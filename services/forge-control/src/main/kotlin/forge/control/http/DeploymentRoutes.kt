package forge.control.http

import forge.control.http.dto.CreateDeploymentRequest
import forge.control.http.dto.toResponse
import forge.control.service.DeploymentService
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.util.UUID

fun Route.deploymentRoutes(deployments: DeploymentService) {
    route("/v1/services/{serviceId}/deployments") {
        post {
            val serviceId = call.parameters.requireUuid("serviceId")
            val body = call.receive<CreateDeploymentRequest>()
            val environmentId = body.environmentId.toUuid("environmentId")
            call.respond(
                HttpStatusCode.Created,
                deployments.create(serviceId, body.image, body.desiredReplicas, environmentId).toResponse(),
            )
        }
        get {
            val serviceId = call.parameters.requireUuid("serviceId")
            call.respond(deployments.list(serviceId).map { it.toResponse() })
        }
    }
    get("/v1/deployments/{deploymentId}") {
        val deploymentId = call.parameters.requireUuid("deploymentId")
        call.respond(deployments.get(deploymentId).toResponse())
    }
}

private fun String?.toUuid(field: String): UUID {
    if (isNullOrBlank()) {
        throw ApiException.BadRequest("$field is required", mapOf("field" to field))
    }
    return try {
        UUID.fromString(this)
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest("$field must be a UUID", mapOf("field" to field))
    }
}
