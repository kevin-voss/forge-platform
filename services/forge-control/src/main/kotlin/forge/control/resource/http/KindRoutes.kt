package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.resource.KindDescriptor
import forge.control.resource.KindRegisterResult
import forge.control.resource.KindRegistry
import forge.control.resource.ResourceScope
import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

/**
 * Cluster-scoped kind registration facade (epic 21+).
 *
 * Controllers call [POST /v1/kinds] at startup; repeats with a compatible
 * descriptor are idempotent. [GET /v1/kinds] lists the in-memory catalog.
 */
fun Route.kindRoutes(
    kinds: KindRegistry,
    log: JsonLog? = null,
) {
    route("/v1/kinds") {
        get {
            call.respond(kinds.all().map { it.toResponse() })
        }
        post {
            val body = call.receive<KindRegistrationRequest>()
            val descriptor = body.toDescriptor()
            when (val result = kinds.registerIdempotent(descriptor)) {
                is KindRegisterResult.Created -> {
                    log?.info(
                        "kind registered",
                        "kind" to descriptor.kind,
                        "plural" to descriptor.plural,
                        "result" to "created",
                        "controller" to descriptor.owningController,
                    )
                    call.respond(HttpStatusCode.Created, result.descriptor.toResponse())
                }
                is KindRegisterResult.AlreadyRegistered -> {
                    log?.info(
                        "kind registered",
                        "kind" to result.descriptor.kind,
                        "plural" to result.descriptor.plural,
                        "result" to "already_registered",
                        "controller" to result.descriptor.owningController,
                    )
                    call.respond(HttpStatusCode.OK, result.descriptor.toResponse())
                }
                is KindRegisterResult.Conflict -> {
                    throw ApiException.Conflict(
                        result.message,
                        details = mapOf(
                            "kind" to descriptor.kind,
                            "plural" to descriptor.plural,
                        ),
                        code = "kind_conflict",
                    )
                }
            }
        }
    }
}

@Serializable
data class KindRegistrationRequest(
    val apiVersion: String = "forge.dev/v1",
    val kind: String,
    val plural: String,
    val scope: String,
    val controller: String,
    val schemaVersion: Int = 1,
    val idPrefix: String? = null,
    val parentKind: String? = null,
    val requiresDeleteConfirmation: Boolean = false,
    val allowsCascade: Boolean = false,
)

@Serializable
data class KindDescriptorResponse(
    val apiVersion: String = "forge.dev/v1",
    val kind: String,
    val plural: String,
    val scope: String,
    val controller: String,
    val schemaVersion: Int,
    val idPrefix: String,
    val parentKind: String? = null,
    val requiresDeleteConfirmation: Boolean = false,
    val allowsCascade: Boolean = false,
)

internal fun KindRegistrationRequest.toDescriptor(): KindDescriptor {
    val kindName = kind.trim()
    val pluralName = plural.trim()
    val controllerName = controller.trim()
    if (kindName.isEmpty()) {
        throw ApiException.BadRequest("kind is required", mapOf("field" to "kind"))
    }
    if (pluralName.isEmpty()) {
        throw ApiException.BadRequest("plural is required", mapOf("field" to "plural"))
    }
    if (controllerName.isEmpty()) {
        throw ApiException.BadRequest("controller is required", mapOf("field" to "controller"))
    }
    if (schemaVersion < 1) {
        throw ApiException.BadRequest("schemaVersion must be >= 1", mapOf("field" to "schemaVersion"))
    }
    val resolvedScope = parseScope(scope)
    val prefix = idPrefix?.trim()?.ifEmpty { null }
        ?: deriveIdPrefix(kindName)
    return KindDescriptor(
        kind = kindName,
        plural = pluralName,
        scope = resolvedScope,
        parentKind = parentKind?.trim()?.ifEmpty { null },
        schemaVersion = schemaVersion,
        owningController = controllerName,
        idPrefix = prefix,
        requiresDeleteConfirmation = requiresDeleteConfirmation,
        allowsCascade = allowsCascade,
        enforceScopeUniqueness = true,
    )
}

internal fun KindDescriptor.toResponse(): KindDescriptorResponse =
    KindDescriptorResponse(
        kind = kind,
        plural = plural,
        scope = scope.toWire(),
        controller = owningController,
        schemaVersion = schemaVersion,
        idPrefix = idPrefix,
        parentKind = parentKind,
        requiresDeleteConfirmation = requiresDeleteConfirmation,
        allowsCascade = allowsCascade,
    )

internal fun parseScope(raw: String): ResourceScope {
    return when (raw.trim().lowercase()) {
        "cluster" -> ResourceScope.Cluster
        "project" -> ResourceScope.Project
        "environment" -> ResourceScope.Environment
        // Controllers may send Kubernetes-style "namespaced" for project/environment kinds.
        "namespaced" -> ResourceScope.Environment
        else -> throw ApiException.BadRequest(
            "scope must be cluster|project|environment|namespaced",
            mapOf("field" to "scope", "value" to raw),
        )
    }
}

internal fun ResourceScope.toWire(): String =
    when (this) {
        ResourceScope.Cluster -> "cluster"
        ResourceScope.Project -> "project"
        ResourceScope.Environment -> "environment"
    }

internal fun deriveIdPrefix(kind: String): String {
    val cleaned = kind.lowercase().filter { it.isLetterOrDigit() }
    return when {
        cleaned.length >= 3 -> cleaned.take(3)
        cleaned.isNotEmpty() -> cleaned
        else -> "res"
    }
}
