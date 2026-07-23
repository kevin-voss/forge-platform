package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.scheduler.DisruptionBudget
import forge.control.scheduler.DisruptionBudgetStore
import forge.control.scheduler.PreemptionAuditor
import forge.control.scheduler.PreemptionPolicy
import forge.control.scheduler.PriorityClass
import forge.control.scheduler.PriorityClassStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.put
import io.ktor.server.routing.route
import java.time.Instant
import java.util.UUID
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class CreatePriorityClassRequest(
    val name: String? = null,
    val value: Int? = null,
    @SerialName("preemption_policy") val preemptionPolicy: String? = null,
    val description: String? = null,
)

@Serializable
data class PriorityClassResponse(
    val name: String,
    val value: Int,
    @SerialName("preemption_policy") val preemptionPolicy: String,
    val description: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
data class UpsertDisruptionBudgetRequest(
    @SerialName("min_available") val minAvailable: Int? = null,
    @SerialName("max_unavailable") val maxUnavailable: Int? = null,
)

@Serializable
data class DisruptionBudgetResponse(
    @SerialName("deployment_id") val deploymentId: String,
    @SerialName("min_available") val minAvailable: Int? = null,
    @SerialName("max_unavailable") val maxUnavailable: Int? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
data class PreemptionEventResponse(
    val id: String,
    @SerialName("victim_placement_id") val victimPlacementId: String,
    @SerialName("preemptor_placement_id") val preemptorPlacementId: String,
    @SerialName("victim_priority") val victimPriority: Int,
    @SerialName("preemptor_priority") val preemptorPriority: Int,
    @SerialName("node_id") val nodeId: String,
    val reason: String,
    @SerialName("created_at") val createdAt: String,
)

fun Route.priorityClassRoutes(store: PriorityClassStore) {
    route("/v1/priority-classes") {
        post {
            val body = call.receive<CreatePriorityClassRequest>()
            val name = body.name?.trim().orEmpty()
            if (name.isEmpty()) {
                throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            }
            val value = body.value
                ?: throw ApiException.BadRequest("value is required", mapOf("field" to "value"))
            val policy = try {
                PreemptionPolicy.parse(body.preemptionPolicy)
            } catch (e: IllegalArgumentException) {
                throw ApiException.BadRequest(
                    e.message ?: "invalid preemption_policy",
                    mapOf("field" to "preemption_policy"),
                )
            }
            val created = store.create(
                name = name,
                value = value,
                preemptionPolicy = policy,
                description = body.description,
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            call.respond(store.list().map { it.toResponse() })
        }
    }
}

fun Route.disruptionBudgetRoutes(store: DisruptionBudgetStore) {
    put("/v1/deployments/{deploymentId}/disruption-budget") {
        val deploymentId = try {
            UUID.fromString(call.parameters["deploymentId"])
        } catch (_: Exception) {
            throw ApiException.BadRequest(
                "deploymentId must be a UUID",
                mapOf("field" to "deploymentId"),
            )
        }
        val body = call.receive<UpsertDisruptionBudgetRequest>()
        if (body.minAvailable == null && body.maxUnavailable == null) {
            throw ApiException.BadRequest(
                "min_available or max_unavailable is required",
                mapOf("field" to "min_available"),
            )
        }
        if (body.minAvailable != null && body.maxUnavailable != null) {
            throw ApiException.BadRequest(
                "set exactly one of min_available or max_unavailable",
                mapOf("field" to "min_available"),
            )
        }
        if (body.minAvailable != null && body.minAvailable < 0) {
            throw ApiException.BadRequest(
                "min_available must be >= 0",
                mapOf("field" to "min_available"),
            )
        }
        if (body.maxUnavailable != null && body.maxUnavailable < 0) {
            throw ApiException.BadRequest(
                "max_unavailable must be >= 0",
                mapOf("field" to "max_unavailable"),
            )
        }
        val budget = store.upsert(
            DisruptionBudget(
                deploymentId = deploymentId,
                minAvailable = body.minAvailable,
                maxUnavailable = body.maxUnavailable,
                createdAt = Instant.now(),
            ),
        )
        call.respond(budget.toResponse())
    }
}

fun Route.preemptionEventRoutes(auditor: PreemptionAuditor) {
    get("/v1/preemption-events") {
        val deploymentRaw = call.request.queryParameters["deployment"]?.trim()
        val deploymentId = if (deploymentRaw.isNullOrEmpty()) {
            null
        } else {
            try {
                UUID.fromString(deploymentRaw)
            } catch (_: IllegalArgumentException) {
                throw ApiException.BadRequest(
                    "deployment must be a UUID",
                    mapOf("field" to "deployment"),
                )
            }
        }
        val events = auditor.list(deploymentId = deploymentId).map {
            PreemptionEventResponse(
                id = it.id,
                victimPlacementId = it.victimPlacementId,
                preemptorPlacementId = it.preemptorPlacementId,
                victimPriority = it.victimPriority,
                preemptorPriority = it.preemptorPriority,
                nodeId = it.nodeId,
                reason = it.reason,
                createdAt = it.createdAt.toString(),
            )
        }
        call.respond(events)
    }
}

private fun PriorityClass.toResponse(): PriorityClassResponse =
    PriorityClassResponse(
        name = name,
        value = value,
        preemptionPolicy = preemptionPolicy.wire(),
        description = description,
        createdAt = createdAt.toString(),
    )

private fun DisruptionBudget.toResponse(): DisruptionBudgetResponse =
    DisruptionBudgetResponse(
        deploymentId = deploymentId.toString(),
        minAvailable = minAvailable,
        maxUnavailable = maxUnavailable,
        createdAt = createdAt.toString(),
    )
