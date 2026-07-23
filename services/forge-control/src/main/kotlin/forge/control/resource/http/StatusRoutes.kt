package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.resource.Condition
import forge.control.resource.ConditionMerge
import forge.control.resource.KindDescriptor
import forge.control.resource.KindRegistry
import forge.control.resource.ResourceRepository
import forge.control.resource.ResourceScope
import forge.control.resource.ResourceVersionGuard
import forge.control.resource.toResponse
import forge.control.telemetry.Telemetry
import io.ktor.server.application.ApplicationCall
import io.ktor.server.request.header
import io.ktor.server.request.receiveText
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.put
import io.ktor.server.routing.route
import io.opentelemetry.api.common.AttributeKey
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.time.Instant

private val statusJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

/**
 * Status subresource for every registered kind.
 *
 * Writes only `status` (+ optional `metadata.resourceVersion`). Spec writes are
 * rejected. Controller identity is a soft convention via `X-Forge-Controller`
 * (temporary until epic 09 service identity); see KindDescriptor.owningController.
 */
fun Route.statusRoutes(
    resources: ResourceRepository,
    kinds: KindRegistry,
    defaultOrganization: String = "default",
    controllerHeaderEnforced: Boolean = true,
    log: JsonLog? = null,
    telemetry: Telemetry = Telemetry.current(),
) {
    route("/v1/{plural}/{name}/status") {
        putStatus(
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
    route("/v1/projects/{project}/{plural}/{name}/status") {
        putStatus(
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
    route("/v1/projects/{project}/environments/{environment}/{plural}/{name}/status") {
        putStatus(
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

private fun Route.putStatus(
    resources: ResourceRepository,
    kinds: KindRegistry,
    controllerHeaderEnforced: Boolean,
    log: JsonLog?,
    telemetry: Telemetry,
    resolveScope: (ApplicationCall, KindDescriptor) -> ScopeCoords,
) {
    put {
        val span = telemetry.startSpan("resource.status_write")
        try {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            enforceControllerHeader(call, kind, controllerHeaderEnforced)

            val raw = call.receiveText()
            val root = statusJson.parseToJsonElement(raw).jsonObject
            if ("spec" in root) {
                throw ApiException.BadRequest(
                    "the /status subresource accepts status only",
                    code = "status_subresource_spec_forbidden",
                )
            }
            val statusElement = root["status"]
                ?: throw ApiException.BadRequest(
                    "status is required",
                    mapOf("field" to "status"),
                )
            if (statusElement !is JsonObject) {
                throw ApiException.BadRequest(
                    "status must be an object",
                    mapOf("field" to "status"),
                )
            }

            val existing = requireExisting(resources, kind, scope, name)
            val expected = root["metadata"]?.jsonObject?.get("resourceVersion")
                ?.let { parseResourceVersion(it.jsonPrimitive.content) }
                ?: throw ApiException.BadRequest(
                    "metadata.resourceVersion is required",
                    mapOf("field" to "metadata.resourceVersion"),
                )
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)

            val now = Instant.now()
            val mergedStatus = mergeStatusConditions(existing.status, statusElement, now)
            val transitionCount = countTransitions(existing.status, mergedStatus)
            val updated = resources.updateStatus(
                id = existing.id,
                expectedVersion = expected,
                status = mergedStatus,
            )

            val phase = mergedStatus["phase"]?.jsonPrimitive?.content
            val observed = mergedStatus["observedGeneration"]?.jsonPrimitive?.content
            log?.info(
                "resource.status_write",
                "kind" to kind.kind,
                "name" to name,
                "controller" to call.request.header("X-Forge-Controller"),
                "phase" to phase,
                "observed_generation" to observed,
            )
            telemetry.recordResourceStatusWrite(kind.kind)
            if (transitionCount > 0) {
                recordConditionTransitions(telemetry, kind.kind, existing.status, mergedStatus)
            }
            span.setAttribute(AttributeKey.stringKey("kind"), kind.kind)
            span.setAttribute(AttributeKey.stringKey("name"), name)
            call.respond(updated.toResponse())
        } finally {
            span.end()
        }
    }
}

/**
 * Soft convention — not authentication. Temporary until epic 09 service identity.
 * Checked against [KindDescriptor.owningController] when enforcement is enabled.
 */
private fun enforceControllerHeader(
    call: ApplicationCall,
    kind: KindDescriptor,
    enforced: Boolean,
) {
    if (!enforced) return
    val header = call.request.header("X-Forge-Controller")?.trim().orEmpty()
    if (header.isEmpty() || header != kind.owningController) {
        throw ApiException.Forbidden(
            "X-Forge-Controller must match owning controller '${kind.owningController}'",
            details = mapOf(
                "owningController" to kind.owningController,
                "provided" to header.ifEmpty { "(missing)" },
            ),
            code = "status_writer_not_recognized",
        )
    }
}

private fun mergeStatusConditions(
    existingStatus: JsonObject,
    incomingStatus: JsonObject,
    now: Instant,
): JsonObject {
    val existingConditions = decodeConditions(existingStatus["conditions"])
    val incomingConditions = decodeConditions(incomingStatus["conditions"])
    if (incomingConditions.isEmpty() && "conditions" !in incomingStatus) {
        return incomingStatus
    }
    val merged = ConditionMerge.mergeConditions(existingConditions, incomingConditions, now)
    val encoded = JsonArray(
        merged.map { condition ->
            buildJsonObject {
                put("type", JsonPrimitive(condition.type))
                put("status", JsonPrimitive(condition.status))
                put("reason", JsonPrimitive(condition.reason))
                put("message", JsonPrimitive(condition.message))
                condition.lastTransitionTime?.let {
                    put("lastTransitionTime", JsonPrimitive(it))
                }
            }
        },
    )
    return JsonObject(incomingStatus + ("conditions" to encoded))
}

private fun decodeConditions(element: kotlinx.serialization.json.JsonElement?): List<Condition> {
    if (element == null || element !is JsonArray) return emptyList()
    return element.mapNotNull { item ->
        if (item !is JsonObject) return@mapNotNull null
        val type = item["type"]?.jsonPrimitive?.content ?: return@mapNotNull null
        val status = item["status"]?.jsonPrimitive?.content ?: return@mapNotNull null
        Condition(
            type = type,
            status = status,
            reason = item["reason"]?.jsonPrimitive?.content.orEmpty(),
            message = item["message"]?.jsonPrimitive?.content.orEmpty(),
            lastTransitionTime = item["lastTransitionTime"]?.jsonPrimitive?.content,
        )
    }
}

private fun countTransitions(before: JsonObject, after: JsonObject): Int {
    val prev = decodeConditions(before["conditions"]).associateBy { it.type }
    val next = decodeConditions(after["conditions"])
    return next.count { c ->
        val old = prev[c.type]
        old == null || old.status != c.status
    }
}

private fun recordConditionTransitions(
    telemetry: Telemetry,
    kind: String,
    before: JsonObject,
    after: JsonObject,
) {
    val prev = decodeConditions(before["conditions"]).associateBy { it.type }
    for (c in decodeConditions(after["conditions"])) {
        val old = prev[c.type]
        if (old == null || old.status != c.status) {
            telemetry.recordResourceConditionTransition(kind, c.type, c.status)
        }
    }
}
