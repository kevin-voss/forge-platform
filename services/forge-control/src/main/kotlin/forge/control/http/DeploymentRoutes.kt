package forge.control.http

import forge.control.http.dto.CreateDeploymentRequest
import forge.control.http.dto.DeploymentStatusReportRequest
import forge.control.http.dto.toResponse
import forge.control.service.DeploymentService
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.util.UUID
import kotlinx.serialization.json.Json

fun Route.deploymentRoutes(deployments: DeploymentService, idempotency: IdempotencyStore? = null) {
    route("/v1/services/{serviceId}/deployments") {
        post {
            val serviceId = call.parameters.requireUuid("serviceId")
            val body = call.receive<CreateDeploymentRequest>()
            val environmentId = body.environmentId.toUuid("environmentId")
            call.idempotentCreate(idempotency, "deployment", Json.encodeToString(CreateDeploymentRequest.serializer(), body)) {
                val created = deployments.create(serviceId, body.image, body.desiredReplicas, environmentId)
                created.id to Json.encodeToJsonElement(forge.control.http.dto.DeploymentResponse.serializer(), created.toResponse())
            }
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
    post("/v1/deployments/{deploymentId}/status") {
        val deploymentId = call.parameters.requireUuid("deploymentId")
        val body = call.receive<DeploymentStatusReportRequest>()
        val updated = deployments.reportStatus(
            deploymentId,
            body.status,
            body.nodeId,
            body.endpoint?.hostPort,
        )
        call.respond(updated.toResponse())
    }
    delete("/v1/deployments/{deploymentId}") {
        val deploymentId = call.parameters.requireUuid("deploymentId")
        deployments.delete(deploymentId)
        call.respond(HttpStatusCode.NoContent)
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
