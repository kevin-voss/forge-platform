package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.repo.RepositoryException
import forge.control.scheduler.LimitsNarrowerThanRequestsException
import forge.control.scheduler.PlaceResult
import forge.control.scheduler.PlacementService
import forge.control.scheduler.RequirementsResolver
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementSpec
import forge.control.scheduler.model.ResourceRequirements
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
            val slotsProvided = body.requirements?.slots
            val slots = slotsProvided ?: 1
            if (slots < 1) {
                throw ApiException.BadRequest(
                    "requirements.slots must be >= 1",
                    mapOf("field" to "requirements.slots"),
                )
            }
            val antiAffinity = try {
                body.antiAffinity?.let { AntiAffinity.parse(it) }
            } catch (e: IllegalArgumentException) {
                throw ApiException.BadRequest(
                    e.message ?: "invalid anti_affinity",
                    mapOf("field" to "anti_affinity"),
                )
            }

            val requirements = ResourceRequirements(
                slots = slots,
                requests = body.requirements?.requests,
                limits = body.requirements?.limits,
                slotsExplicit = slotsProvided != null,
            )
            try {
                RequirementsResolver.resolve(requirements)
            } catch (e: LimitsNarrowerThanRequestsException) {
                throw ApiException.BadRequest(
                    e.message ?: "limits narrower than requests",
                    mapOf("field" to "requirements.limits"),
                    code = "limits_narrower_than_requests",
                )
            }

            val placementSpec = PlacementSpec(
                nodeSelector = body.placement?.resolvedNodeSelector().orEmpty(),
                tolerations = body.placement?.resolvedTolerations().orEmpty(),
            )
            val result = try {
                placements.placeAndPersist(
                    deploymentId = deploymentId,
                    replicaIndex = replicaIndex,
                    serviceId = body.serviceId?.trim()?.takeIf { it.isNotEmpty() },
                    requirements = requirements,
                    antiAffinity = antiAffinity,
                    placement = placementSpec,
                    platform = body.platform,
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
                is PlaceResult.QueueFull ->
                    throw ApiException.Conflict(
                        "placement queue full (max=${result.maxLen})",
                        mapOf("code" to "queue_full", "max_len" to result.maxLen.toString()),
                        code = "queue_full",
                    )
                is PlaceResult.Pending ->
                    call.respond(
                        if (result.created) HttpStatusCode.Accepted else HttpStatusCode.OK,
                        result.placement.toResponse(),
                    )
                is PlaceResult.Ok ->
                    call.respond(
                        if (result.created) HttpStatusCode.Created else HttpStatusCode.OK,
                        result.placement.toResponse(),
                    )
            }
        }
        get {
            val deploymentRaw = call.request.queryParameters["deployment"]?.trim()?.takeIf { it.isNotEmpty() }
            val status = call.request.queryParameters["status"]?.trim()?.lowercase()
            if (status != null && status !in setOf("placed", "pending", "lost")) {
                throw ApiException.BadRequest(
                    "status must be placed|pending|lost",
                    mapOf("field" to "status"),
                )
            }
            // Cluster-wide pending query (epic 24 node autoscaler): allow omitting
            // deployment when status=pending. Otherwise deployment remains required.
            if (deploymentRaw == null) {
                if (status != "pending") {
                    throw ApiException.BadRequest(
                        "deployment query parameter is required unless status=pending",
                        mapOf("field" to "deployment"),
                    )
                }
                call.respond(placements.listPending().map { it.toResponse() })
                return@get
            }
            val deploymentId = parseDeploymentId(deploymentRaw)
            call.respond(placements.list(deploymentId, status).map { it.toResponse() })
        }
        get("/{id}") {
            val id = call.parameters["id"]?.trim().orEmpty()
            if (id.isEmpty()) {
                throw ApiException.BadRequest(
                    "placement id is required",
                    mapOf("field" to "id"),
                )
            }
            val placement = placements.get(id)
                ?: throw ApiException.NotFound(
                    "placement not found",
                    mapOf("placement_id" to id),
                )
            call.respond(placement.toResponse())
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
