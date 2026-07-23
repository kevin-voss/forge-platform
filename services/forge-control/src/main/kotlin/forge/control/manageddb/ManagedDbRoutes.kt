package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.http.idempotentCreate
import forge.control.http.requireUuid
import forge.control.repo.IdempotencyStore
import io.ktor.server.application.ApplicationCall
import io.ktor.server.request.header
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.json.Json
import java.util.UUID

private const val PROJECT_HEADER = "X-Forge-Project"

fun Route.managedDbRoutes(managedDb: ManagedDbService, idempotency: IdempotencyStore? = null) {
    route("/v1/databases/instances") {
        post {
            val body = call.receive<CreateDbInstanceRequest>()
            val projectId = call.resolveProjectId(body.projectId)
            call.idempotentCreate(
                idempotency,
                "db_instance",
                Json.encodeToString(CreateDbInstanceRequest.serializer(), body),
            ) {
                val created = managedDb.createInstance(projectId, body.name)
                created.id to Json.encodeToJsonElement(
                    DbInstanceResponse.serializer(),
                    created.toResponse(),
                )
            }
        }
        get {
            val projectId = call.resolveProjectId(call.request.queryParameters["projectId"])
            call.respond(managedDb.listInstances(projectId).map { it.toResponse() })
        }
    }

    get("/v1/databases/instances/{instanceId}") {
        val instanceId = call.parameters.requireUuid("instanceId")
        call.respond(managedDb.getInstance(instanceId).toResponse())
    }

    route("/v1/databases/instances/{instanceId}/databases") {
        get {
            val instanceId = call.parameters.requireUuid("instanceId")
            call.respond(managedDb.listDatabases(instanceId))
        }
        post {
            val instanceId = call.parameters.requireUuid("instanceId")
            val body = call.receive<CreateDbDatabaseRequest>()
            call.idempotentCreate(
                idempotency,
                "db_database",
                Json.encodeToString(CreateDbDatabaseRequest.serializer(), body) + "|$instanceId",
            ) {
                val created = managedDb.createDatabase(instanceId, body.name)
                created.database.id to Json.encodeToJsonElement(
                    DbDatabaseResponse.serializer(),
                    managedDb.toCreateResponse(created),
                )
            }
        }
    }

    get("/v1/databases/{databaseId}") {
        val databaseId = call.parameters.requireUuid("databaseId")
        call.respond(managedDb.getDatabase(databaseId))
    }
}

private fun ApplicationCall.resolveProjectId(explicit: String?): UUID {
    val raw = explicit?.trim()?.takeIf { it.isNotEmpty() }
        ?: request.header(PROJECT_HEADER)?.trim()?.takeIf { it.isNotEmpty() }
        ?: throw ApiException.BadRequest(
            "projectId is required (body/query projectId or X-Forge-Project header)",
            mapOf("field" to "projectId"),
        )
    return try {
        UUID.fromString(raw)
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest(
            "invalid UUID for projectId",
            mapOf("field" to "projectId"),
        )
    }
}
