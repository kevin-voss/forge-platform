package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.resource.FinalizerPatchRequest
import forge.control.resource.Finalizers
import forge.control.resource.KindDescriptor
import forge.control.resource.KindRegistry
import forge.control.resource.ResourceRepository
import forge.control.resource.ResourceScope
import forge.control.resource.toResponse
import forge.control.telemetry.Telemetry
import io.ktor.server.application.ApplicationCall
import io.ktor.server.request.header
import io.ktor.server.request.receiveText
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.patch
import io.ktor.server.routing.route
import kotlinx.serialization.json.Json

private val finalizerJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

/**
 * Controller-facing finalizer mutation API.
 *
 * Removals require `X-Forge-Controller` matching [KindDescriptor.owningController]
 * (soft convention until epic 09 service identity).
 */
fun Route.finalizerRoutes(
    resources: ResourceRepository,
    kinds: KindRegistry,
    defaultOrganization: String = "default",
    controllerHeaderEnforced: Boolean = true,
    log: JsonLog? = null,
    telemetry: Telemetry = Telemetry.current(),
) {
    route("/v1/{plural}/{name}/finalizers") {
        patchFinalizers(
            resources = resources,
            kinds = kinds,
            controllerHeaderEnforced = controllerHeaderEnforced,
            log = log,
            telemetry = telemetry,
            resolveScope = { _, kind ->
                requireScope(kind, ResourceScope.Cluster)
                ScopeCoords(organization = defaultOrganization, project = null, environment = null)
            },
        )
    }
    route("/v1/projects/{project}/{plural}/{name}/finalizers") {
        patchFinalizers(
            resources = resources,
            kinds = kinds,
            controllerHeaderEnforced = controllerHeaderEnforced,
            log = log,
            telemetry = telemetry,
            resolveScope = { call, kind ->
                requireScope(kind, ResourceScope.Project)
                ScopeCoords(
                    organization = defaultOrganization,
                    project = call.parameters["project"],
                    environment = null,
                )
            },
        )
    }
    route("/v1/projects/{project}/environments/{environment}/{plural}/{name}/finalizers") {
        patchFinalizers(
            resources = resources,
            kinds = kinds,
            controllerHeaderEnforced = controllerHeaderEnforced,
            log = log,
            telemetry = telemetry,
            resolveScope = { call, kind ->
                requireScope(kind, ResourceScope.Environment)
                ScopeCoords(
                    organization = defaultOrganization,
                    project = call.parameters["project"],
                    environment = call.parameters["environment"],
                )
            },
        )
    }
}

private fun Route.patchFinalizers(
    resources: ResourceRepository,
    kinds: KindRegistry,
    controllerHeaderEnforced: Boolean,
    log: JsonLog?,
    telemetry: Telemetry,
    resolveScope: (ApplicationCall, KindDescriptor) -> ScopeCoords,
) {
    patch {
        val kind = resolveKind(kinds, call.parameters["plural"])
        val scope = resolveScope(call, kind)
        val name = call.parameters["name"]
            ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
        val existing = requireExisting(resources, kind, scope, name)
        val raw = call.receiveText()
        val patch = finalizerJson.decodeFromString(FinalizerPatchRequest.serializer(), raw)
        if (patch.remove.isNotEmpty()) {
            enforceFinalizerController(call, kind, controllerHeaderEnforced)
        }
        val before = Finalizers.asStrings(existing.finalizers).toSet()
        val next = Finalizers.applyPatch(existing.finalizers, patch)
        val after = Finalizers.asStrings(next).toSet()
        val actor = call.request.header("X-Forge-Controller")?.trim().orEmpty()
            .ifEmpty { "anonymous" }
        for (added in after - before) {
            log?.info(
                "resource.finalizer_add",
                "resource_id" to existing.id,
                "finalizer" to added,
                "actor" to actor,
            )
        }
        for (removed in before - after) {
            log?.info(
                "resource.finalizer_remove",
                "resource_id" to existing.id,
                "finalizer" to removed,
                "actor" to actor,
            )
        }
        val updated = resources.replaceFinalizers(existing.id, next)
        val terminal = updated.deletedAt != null
        logWrite(
            log = log,
            telemetry = telemetry,
            kind = kind.kind,
            name = name,
            scope = scope,
            action = if (terminal) "delete" else "finalizers",
            oldVersion = existing.resourceVersion,
            newVersion = updated.resourceVersion,
        )
        if (terminal) {
            call.respond(io.ktor.http.HttpStatusCode.NoContent)
        } else {
            call.respond(updated.toResponse())
        }
    }
}

private fun enforceFinalizerController(
    call: ApplicationCall,
    kind: KindDescriptor,
    enforced: Boolean,
) {
    if (!enforced) return
    val header = call.request.header("X-Forge-Controller")?.trim().orEmpty()
    if (header.isEmpty() || header != kind.owningController) {
        throw ApiException.Forbidden(
            "finalizer removal requires X-Forge-Controller='${kind.owningController}'",
            details = mapOf(
                "owningController" to kind.owningController,
                "provided" to header.ifEmpty { "(missing)" },
            ),
            code = "forbidden_finalizer",
        )
    }
}

// Local copy of write logging to avoid widening ResourceRoutes visibility further.
private fun logWrite(
    log: JsonLog?,
    telemetry: Telemetry,
    kind: String,
    name: String,
    scope: ScopeCoords,
    action: String,
    oldVersion: Long?,
    newVersion: Long,
) {
    log?.info(
        "resource.write",
        "kind" to kind,
        "name" to name,
        "project" to scope.project,
        "environment" to scope.environment,
        "action" to action,
        "resource_version_old" to oldVersion,
        "resource_version_new" to newVersion,
    )
    telemetry.recordResourceWrite(kind, action)
}
