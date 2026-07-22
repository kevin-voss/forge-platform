package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.repo.RepositoryException
import forge.control.scheduler.PlaceResult
import forge.control.scheduler.PlacementService
import forge.control.scheduler.model.AntiAffinity
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.util.UUID

fun Route.placementRoutes(placements: PlacementService) {
    route("/v1/placements") {
        post {
            val body = call.receive<CreatePlacementRequest>()
            val deploymentId = parseDeploymentId(body.deploymentId)
            val replicaIndex = body.replicaIndex
                ?: throw ApiException.BadRequest(
                    "replica_index is required",
                    mapOf("field" to "replica_index"),
                )
            if (replicaIndex < 0) {
                throw ApiException.BadRequest(
                    "replica_index must be >= 0",
                    mapOf("field" to "replica_index"),
                )
            }
            val slots = body.requirements?.slots ?: 1
            if (slots < 1) {
                throw ApiException.BadRequest(
                    "requirements.slots must be >= 1",
                    mapOf("field" to "requirements.slots"),
                )
            }
            val antiAffinity = try {
                AntiAffinity.parse(body.antiAffinity)
            } catch (e: IllegalArgumentException) {
                throw ApiException.BadRequest(
                    e.message ?: "invalid anti_affinity",
                    mapOf("field" to "anti_affinity"),
                )
            }

            val result = try {
                placements.placeAndPersist(
                    deploymentId = deploymentId,
                    replicaIndex = replicaIndex,
                    slots = slots,
                    antiAffinity = antiAffinity,
                )
            } catch (_: RepositoryException.ConstraintViolation) {
                throw ApiException.NotFound(
                    "deployment not found",
                    mapOf("deployment_id" to deploymentId.toString()),
                )
            }
            when (result) {
                is PlaceResult.NoNode ->
                    throw ApiException.Conflict(
                        result.reason,
                        mapOf("code" to "no_node_available"),
                        code = "no_node_available",
                    )
                is PlaceResult.Ok ->
                    call.respond(
                        if (result.created) HttpStatusCode.Created else HttpStatusCode.OK,
                        result.placement.toResponse(),
                    )
            }
        }
        get {
            val deploymentRaw = call.request.queryParameters["deployment"]
                ?: throw ApiException.BadRequest(
                    "deployment query parameter is required",
                    mapOf("field" to "deployment"),
                )
            val deploymentId = parseDeploymentId(deploymentRaw)
            call.respond(placements.list(deploymentId).map { it.toResponse() })
        }
    }
}

private fun parseDeploymentId(raw: String?): UUID {
    if (raw.isNullOrBlank()) {
        throw ApiException.BadRequest(
            "deployment_id is required",
            mapOf("field" to "deployment_id"),
        )
    }
    return try {
        UUID.fromString(raw.trim())
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest(
            "deployment_id must be a UUID",
            mapOf("field" to "deployment_id"),
        )
    }
}
