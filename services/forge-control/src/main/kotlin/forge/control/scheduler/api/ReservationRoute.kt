package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.scheduler.CapacityHold
import forge.control.scheduler.MigrationApproval
import forge.control.scheduler.ReservationResources
import forge.control.scheduler.ReservationService
import forge.control.scheduler.StatefulPrimaryGuard
import forge.control.scheduler.model.GpuRequest
import forge.control.scheduler.model.ResourceQuantity
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.time.Duration
import java.util.UUID
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class CreateReservationRequest(
    val name: String? = null,
    val resources: ReservationResourcesDto? = null,
    @SerialName("expires_after") val expiresAfter: String? = null,
    @SerialName("expiresAfter") val expiresAfterCamel: String? = null,
    @SerialName("owner_ref") val ownerRef: String? = null,
    @SerialName("node_id") val nodeId: String? = null,
)

@Serializable
data class ReservationResourcesDto(
    val cpu: String? = null,
    val memory: String? = null,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("memory_mb") val memoryMb: Int? = null,
    val slots: Int? = null,
    val gpu: GpuRequest? = null,
) {
    fun toModel(): ReservationResources =
        ReservationResources(
            cpu = cpu,
            memory = memory,
            cpuMillis = cpuMillis,
            memoryMb = memoryMb,
            slots = slots,
            gpu = gpu,
        )
}

@Serializable
data class ReservationResponse(
    val name: String,
    val resources: ReservationResourcesDto,
    @SerialName("expires_at") val expiresAt: String,
    @SerialName("owner_ref") val ownerRef: String? = null,
    @SerialName("node_id") val nodeId: String? = null,
    val status: String,
    @SerialName("created_at") val createdAt: String,
    @SerialName("consumed_by_placement_id") val consumedByPlacementId: String? = null,
)

@Serializable
data class ApproveMigrationRequest(
    @SerialName("replica_index") val replicaIndex: Int? = null,
    @SerialName("approved_by") val approvedBy: String? = null,
)

@Serializable
data class MigrationApprovalResponse(
    @SerialName("deployment_id") val deploymentId: String,
    @SerialName("replica_index") val replicaIndex: Int,
    @SerialName("approved_at") val approvedAt: String,
    @SerialName("approved_by") val approvedBy: String? = null,
)

fun Route.reservationRoutes(service: ReservationService) {
    route("/v1/reservations") {
        post {
            val body = call.receive<CreateReservationRequest>()
            val name = body.name?.trim().orEmpty()
            if (name.isEmpty()) {
                throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            }
            val resources = body.resources?.toModel()
                ?: throw ApiException.BadRequest(
                    "resources is required",
                    mapOf("field" to "resources"),
                )
            val expiresRaw = body.expiresAfter ?: body.expiresAfterCamel
            val expires = parseDuration(expiresRaw)
                ?: throw ApiException.BadRequest(
                    "expires_after is required (e.g. 30m)",
                    mapOf("field" to "expires_after"),
                )
            val created = service.create(
                name = name,
                resources = resources,
                expiresAfter = expires,
                ownerRef = body.ownerRef?.trim()?.takeIf { it.isNotEmpty() },
                preferredNodeId = body.nodeId?.trim()?.takeIf { it.isNotEmpty() },
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            call.respond(service.listActive().map { it.toResponse() })
        }
        get("/{name}") {
            val name = call.parameters["name"]?.trim().orEmpty()
            val hold = service.find(name)
                ?: throw ApiException.NotFound(
                    "reservation not found",
                    mapOf("name" to name),
                )
            call.respond(hold.toResponse())
        }
    }
}

fun Route.migrationApprovalRoutes(guard: StatefulPrimaryGuard) {
    post("/v1/deployments/{deploymentId}/migration-approvals") {
        val deploymentId = try {
            UUID.fromString(call.parameters["deploymentId"])
        } catch (_: Exception) {
            throw ApiException.BadRequest(
                "invalid deployment id",
                mapOf("field" to "deploymentId"),
            )
        }
        val body = call.receive<ApproveMigrationRequest>()
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
        val approved = guard.approveMigration(
            deploymentId = deploymentId,
            replicaIndex = replicaIndex,
            approvedBy = body.approvedBy,
        )
        call.respond(HttpStatusCode.Created, approved.toResponse())
    }
}

private fun parseDuration(raw: String?): Duration? {
    if (raw.isNullOrBlank()) return null
    val s = raw.trim().lowercase()
    return try {
        when {
            s.endsWith("ms") -> Duration.ofMillis(s.dropLast(2).trim().toLong())
            s.endsWith("s") -> Duration.ofSeconds(s.dropLast(1).trim().toLong())
            s.endsWith("m") -> Duration.ofMinutes(s.dropLast(1).trim().toLong())
            s.endsWith("h") -> Duration.ofHours(s.dropLast(1).trim().toLong())
            else -> Duration.parse(s)
        }
    } catch (_: Exception) {
        null
    }
}

private fun CapacityHold.toResponse(): ReservationResponse =
    ReservationResponse(
        name = name,
        resources = ReservationResourcesDto(
            cpu = resources.cpu ?: resources.cpuMillis?.let { ResourceQuantity.formatCpuMillis(it) },
            memory = resources.memory
                ?: resources.memoryMb?.let { ResourceQuantity.formatMemoryMb(it) },
            cpuMillis = resources.cpuMillis,
            memoryMb = resources.memoryMb,
            slots = resources.slots,
            gpu = resources.gpu,
        ),
        expiresAt = expiresAt.toString(),
        ownerRef = ownerRef,
        nodeId = nodeId,
        status = status,
        createdAt = createdAt.toString(),
        consumedByPlacementId = consumedByPlacementId,
    )

private fun MigrationApproval.toResponse(): MigrationApprovalResponse =
    MigrationApprovalResponse(
        deploymentId = deploymentId.toString(),
        replicaIndex = replicaIndex,
        approvedAt = approvedAt.toString(),
        approvedBy = approvedBy,
    )
