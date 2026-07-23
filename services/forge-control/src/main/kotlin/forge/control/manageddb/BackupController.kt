package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.http.idempotentAction
import forge.control.http.requireUuid
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
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

/** HTTP routes for managed-db backup + restore (18.04). */
fun Route.backupRoutes(managedDb: ManagedDbService, idempotency: IdempotencyStore? = null) {
    route("/v1/databases/{databaseId}/backups") {
        post {
            val databaseId = call.parameters.requireUuid("databaseId")
            val projectId = call.requireProjectHeader()
            call.idempotentAction(
                idempotency,
                "db_backup",
                "backup|$databaseId|$projectId",
                HttpStatusCode.Accepted,
            ) {
                val created = managedDb.createBackup(databaseId, projectId)
                created.id to Json.encodeToJsonElement(
                    DbBackupResponse.serializer(),
                    created.toResponse(),
                )
            }
        }
        get {
            val databaseId = call.parameters.requireUuid("databaseId")
            val projectId = call.requireProjectHeader()
            call.respond(managedDb.listBackups(databaseId, projectId).map { it.toResponse() })
        }
        get("/{backupId}") {
            val databaseId = call.parameters.requireUuid("databaseId")
            val backupId = call.parameters.requireUuid("backupId")
            val projectId = call.requireProjectHeader()
            call.respond(managedDb.getBackup(databaseId, backupId, projectId).toResponse())
        }
    }

    post("/v1/databases/backups/{backupId}/restore") {
        val backupId = call.parameters.requireUuid("backupId")
        val projectId = call.requireProjectHeader()
        val body = call.receive<RestoreBackupRequest>()
        call.idempotentAction(
            idempotency,
            "db_restore",
            Json.encodeToString(RestoreBackupRequest.serializer(), body) + "|$backupId|$projectId",
            HttpStatusCode.Accepted,
        ) {
            val accepted = managedDb.restoreBackup(backupId, body.targetDatabaseId, projectId)
            backupId to Json.encodeToJsonElement(
                RestoreBackupResponse.serializer(),
                accepted,
            )
        }
    }
}

private fun ApplicationCall.requireProjectHeader(): UUID {
    val raw = request.header(PROJECT_HEADER)?.trim()?.takeIf { it.isNotEmpty() }
        ?: throw ApiException.BadRequest(
            "X-Forge-Project header is required",
            mapOf("field" to "X-Forge-Project"),
        )
    return try {
        UUID.fromString(raw)
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest(
            "invalid UUID for X-Forge-Project",
            mapOf("field" to "X-Forge-Project"),
        )
    }
}
