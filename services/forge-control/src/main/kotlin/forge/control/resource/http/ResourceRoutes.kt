package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.http.idempotentCreate
import forge.control.logging.JsonLog
import forge.control.repo.IdempotencyStore
import forge.control.repo.RepositoryException
import forge.control.resource.JsonPatch
import forge.control.resource.KindDescriptor
import forge.control.resource.KindRegistry
import forge.control.resource.MergePatch
import forge.control.resource.NewResourceRow
import forge.control.resource.ResourceEnvelopeResponse
import forge.control.resource.ResourceRepository
import forge.control.resource.ResourceScope
import forge.control.resource.ResourceVersionGuard
import forge.control.resource.ResourceWriteRequest
import forge.control.resource.Ulid
import forge.control.resource.toResponse
import forge.control.telemetry.Telemetry
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.server.application.ApplicationCall
import io.ktor.server.request.contentType
import io.ktor.server.request.receive
import io.ktor.server.request.receiveText
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.patch
import io.ktor.server.routing.post
import io.ktor.server.routing.put
import io.ktor.server.routing.route
import io.opentelemetry.api.common.AttributeKey
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

private val resourceJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

/**
 * Generic CRUD for every kind in [KindRegistry], dispatched by `{plural}` + scope.
 */
fun Route.resourceRoutes(
    resources: ResourceRepository,
    kinds: KindRegistry,
    idempotency: IdempotencyStore? = null,
    defaultOrganization: String = "default",
    log: JsonLog? = null,
    telemetry: Telemetry = Telemetry.current(),
) {
    // Cluster: /v1/{plural}
    route("/v1/{plural}") {
        collectionRoutes(
            resources = resources,
            kinds = kinds,
            idempotency = idempotency,
            defaultOrganization = defaultOrganization,
            log = log,
            telemetry = telemetry,
            resolveScope = { _, kind ->
                requireScope(kind, ResourceScope.Cluster)
                ScopeCoords(organization = defaultOrganization, project = null, environment = null)
            },
        )
        itemRoutes(
            resources = resources,
            kinds = kinds,
            defaultOrganization = defaultOrganization,
            log = log,
            telemetry = telemetry,
            resolveScope = { _, kind ->
                requireScope(kind, ResourceScope.Cluster)
                ScopeCoords(organization = defaultOrganization, project = null, environment = null)
            },
        )
    }

    // Project: /v1/projects/{project}/{plural}
    route("/v1/projects/{project}/{plural}") {
        collectionRoutes(
            resources = resources,
            kinds = kinds,
            idempotency = idempotency,
            defaultOrganization = defaultOrganization,
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
        itemRoutes(
            resources = resources,
            kinds = kinds,
            defaultOrganization = defaultOrganization,
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

    // Environment: /v1/projects/{project}/environments/{environment}/{plural}
    route("/v1/projects/{project}/environments/{environment}/{plural}") {
        collectionRoutes(
            resources = resources,
            kinds = kinds,
            idempotency = idempotency,
            defaultOrganization = defaultOrganization,
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
        itemRoutes(
            resources = resources,
            kinds = kinds,
            defaultOrganization = defaultOrganization,
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

private fun Route.collectionRoutes(
    resources: ResourceRepository,
    kinds: KindRegistry,
    idempotency: IdempotencyStore?,
    defaultOrganization: String,
    log: JsonLog?,
    telemetry: Telemetry,
    resolveScope: (ApplicationCall, KindDescriptor) -> ScopeCoords,
) {
    post {
        val kind = resolveKind(kinds, call.parameters["plural"])
        val scope = resolveScope(call, kind)
        val raw = call.receiveText()
        val body = resourceJson.decodeFromString(ResourceWriteRequest.serializer(), raw)
        call.idempotentCreate(idempotency, "resource:${kind.kind}", raw) {
            val created = createResource(
                resources = resources,
                kind = kind,
                scope = scope,
                body = body,
                defaultOrganization = defaultOrganization,
            )
            logWrite(log, telemetry, kind.kind, created.name, scope, "create", null, created.resourceVersion)
            created.id to resourceJson.encodeToJsonElement(
                ResourceEnvelopeResponse.serializer(),
                created.toResponse(),
            )
        }
    }
}

private fun Route.itemRoutes(
    resources: ResourceRepository,
    kinds: KindRegistry,
    defaultOrganization: String,
    log: JsonLog?,
    telemetry: Telemetry,
    resolveScope: (ApplicationCall, KindDescriptor) -> ScopeCoords,
) {
    route("/{name}") {
        get {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            val row = resources.findByScopeAndName(
                kind = kind.kind,
                organization = scope.organization,
                project = scope.project,
                environment = scope.environment,
                name = name,
            ) ?: throw ApiException.NotFound(
                "resource not found",
                details = mapOf("kind" to kind.kind, "name" to name),
                code = "not_found",
            )
            call.respond(row.toResponse())
        }
        put {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            val body = call.receive<ResourceWriteRequest>()
            val existing = requireExisting(resources, kind, scope, name)
            val expected = parseResourceVersion(body.metadata?.resourceVersion)
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)
            val labels = body.metadata?.labels ?: existing.labels
            val annotations = body.metadata?.annotations ?: existing.annotations
            val ownerRefs = body.metadata?.ownerRefs ?: existing.ownerRefs
            val finalizers = body.metadata?.finalizers ?: existing.finalizers
            val updated = try {
                resources.replace(
                    id = existing.id,
                    expectedVersion = expected,
                    labels = labels,
                    annotations = annotations,
                    spec = body.spec,
                    ownerRefs = ownerRefs,
                    finalizers = finalizers,
                )
            } catch (e: RepositoryException.Conflict) {
                throw mapRepo(e)
            }
            logWrite(log, telemetry, kind.kind, name, scope, "replace", expected, updated.resourceVersion)
            call.respond(updated.toResponse())
        }
        patch {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            val existing = requireExisting(resources, kind, scope, name)
            val contentType = call.request.contentType()
            val raw = call.receiveText()
            val (labels, annotations, spec, expected) = applyPatch(
                existing = existing,
                contentType = contentType,
                raw = raw,
            )
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)
            val updated = resources.patch(
                id = existing.id,
                expectedVersion = expected,
                labels = labels,
                annotations = annotations,
                spec = spec,
            )
            logWrite(log, telemetry, kind.kind, name, scope, "patch", expected, updated.resourceVersion)
            call.respond(updated.toResponse())
        }
        delete {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            val existing = requireExisting(resources, kind, scope, name)
            val deleted = resources.softDelete(existing.id)
            logWrite(
                log,
                telemetry,
                kind.kind,
                name,
                scope,
                "delete",
                existing.resourceVersion,
                deleted.resourceVersion,
            )
            call.respond(HttpStatusCode.NoContent)
        }
    }
}

private data class ScopeCoords(
    val organization: String,
    val project: String?,
    val environment: String?,
)

private data class PatchResult(
    val labels: JsonObject,
    val annotations: JsonObject,
    val spec: JsonObject,
    val expectedVersion: Long,
)

private fun resolveKind(kinds: KindRegistry, plural: String?): KindDescriptor {
    if (plural.isNullOrBlank()) {
        throw ApiException.BadRequest("plural is required", mapOf("field" to "plural"))
    }
    return kinds.byPlural(plural)
        ?: throw ApiException.NotFound(
            "kind not registered for plural '$plural'",
            details = mapOf("plural" to plural),
            code = "kind_not_registered",
        )
}

private fun requireScope(kind: KindDescriptor, expected: ResourceScope) {
    if (kind.scope != expected) {
        throw ApiException.NotFound(
            "kind '${kind.kind}' is not ${expected.name.lowercase()}-scoped",
            details = mapOf("kind" to kind.kind, "scope" to kind.scope.name),
            code = "kind_not_registered",
        )
    }
}

private fun requireExisting(
    resources: ResourceRepository,
    kind: KindDescriptor,
    scope: ScopeCoords,
    name: String,
) = resources.findByScopeAndName(
    kind = kind.kind,
    organization = scope.organization,
    project = scope.project,
    environment = scope.environment,
    name = name,
) ?: throw ApiException.NotFound(
    "resource not found",
    details = mapOf("kind" to kind.kind, "name" to name),
    code = "not_found",
)

private fun createResource(
    resources: ResourceRepository,
    kind: KindDescriptor,
    scope: ScopeCoords,
    body: ResourceWriteRequest,
    defaultOrganization: String,
): forge.control.resource.ResourceRow {
    ResourceVersionGuard.acceptCreate()
    val name = body.metadata?.name?.trim().orEmpty()
    if (name.isEmpty()) {
        throw ApiException.BadRequest("metadata.name is required", mapOf("field" to "metadata.name"))
    }
    if (body.kind != null && body.kind != kind.kind) {
        throw ApiException.BadRequest(
            "kind mismatch: body=${body.kind}, path=${kind.kind}",
            mapOf("field" to "kind"),
        )
    }
    val organization = body.metadata?.organization?.trim().orEmpty().ifEmpty { defaultOrganization }
    val apiVersion = body.apiVersion?.trim().orEmpty().ifEmpty { "forge.dev/v1" }
    return try {
        resources.insert(
            NewResourceRow(
                id = Ulid.next(kind.idPrefix),
                kind = kind.kind,
                apiVersion = apiVersion,
                organization = organization,
                project = scope.project,
                environment = scope.environment,
                name = name,
                labels = body.metadata?.labels ?: JsonObject(emptyMap()),
                annotations = body.metadata?.annotations ?: JsonObject(emptyMap()),
                spec = body.spec,
                ownerRefs = body.metadata?.ownerRefs ?: JsonArray(emptyList()),
                finalizers = body.metadata?.finalizers ?: JsonArray(emptyList()),
            ),
        )
    } catch (e: RepositoryException.Conflict) {
        throw ApiException.Conflict(e.message ?: "resource already exists", code = "conflict")
    } catch (e: RepositoryException.ConstraintViolation) {
        throw ApiException.BadRequest(e.message ?: "constraint violation")
    }
}

private fun applyPatch(
    existing: forge.control.resource.ResourceRow,
    contentType: ContentType,
    raw: String,
): PatchResult {
    val mime = "${contentType.contentType}/${contentType.contentSubtype}".lowercase()
    val isJsonPatch = mime == "application/json-patch+json"
    val isMergePatch = mime == "application/merge-patch+json" ||
        mime == "application/json" ||
        mime == "*/*" ||
        contentType == ContentType.Any ||
        contentType.contentSubtype.isBlank()

    return if (isJsonPatch) {
        val ops = resourceJson.parseToJsonElement(raw).jsonArray
        // Patch against a reduced document with only writable fields + metadata wrapper.
        val patchable = JsonObject(
            mapOf(
                "spec" to existing.spec,
                "metadata" to JsonObject(
                    mapOf(
                        "labels" to existing.labels,
                        "annotations" to existing.annotations,
                        "resourceVersion" to JsonPrimitive(existing.resourceVersion.toString()),
                    ),
                ),
            ),
        )
        val patched = JsonPatch.apply(patchable, ops)
        val metadata = patched["metadata"]?.jsonObject
        // resourceVersion for concurrency may be supplied via a merge-style wrapper; for JSON Patch
        // clients send If-Match semantics via a replace on metadata.resourceVersion or we require
        // the current version embedded — accept optional /metadata/resourceVersion test/replace.
        val expected = metadata?.get("resourceVersion")
            ?.let { el ->
                parseResourceVersion(el.jsonPrimitive.content)
            }
            ?: existing.resourceVersion
        PatchResult(
            labels = metadata?.get("labels")?.jsonObject ?: existing.labels,
            annotations = metadata?.get("annotations")?.jsonObject ?: existing.annotations,
            spec = patched["spec"]?.jsonObject ?: existing.spec,
            expectedVersion = expected,
        )
    } else if (isMergePatch) {
        val patch = resourceJson.parseToJsonElement(raw).jsonObject
        val expected = patch["metadata"]?.jsonObject?.get("resourceVersion")
            ?.let { el -> parseResourceVersion(el.jsonPrimitive.content) }
            ?: existing.resourceVersion
        val mergedSpec = patch["spec"]?.let {
            if (it is JsonObject) MergePatch.apply(existing.spec, it) else existing.spec
        } ?: existing.spec
        val metaPatch = patch["metadata"]?.jsonObject
        val labels = metaPatch?.get("labels")?.let {
            if (it is JsonObject) MergePatch.apply(existing.labels, it) else existing.labels
        } ?: existing.labels
        val annotations = metaPatch?.get("annotations")?.let {
            if (it is JsonObject) MergePatch.apply(existing.annotations, it) else existing.annotations
        } ?: existing.annotations
        // Also allow top-level merge of the whole envelope shape used in contracts:
        // { "spec": { ... } } without metadata.resourceVersion uses current version.
        PatchResult(labels, annotations, mergedSpec, expected)
    } else {
        throw ApiException.BadRequest(
            "unsupported Content-Type for PATCH: $mime",
            details = mapOf("contentType" to mime),
            code = "invalid_request",
        )
    }
}

private fun parseResourceVersion(raw: String?): Long {
    if (raw.isNullOrBlank()) {
        throw ApiException.BadRequest(
            "metadata.resourceVersion is required",
            mapOf("field" to "metadata.resourceVersion"),
        )
    }
    return raw.toLongOrNull()
        ?: throw ApiException.BadRequest(
            "metadata.resourceVersion must be an integer",
            mapOf("field" to "metadata.resourceVersion"),
        )
}

private fun mapRepo(e: RepositoryException): ApiException =
    when (e) {
        is RepositoryException.Conflict -> ApiException.Conflict(e.message ?: "conflict")
        is RepositoryException.NotFound -> ApiException.NotFound(e.message ?: "not found")
        is RepositoryException.ConstraintViolation ->
            ApiException.BadRequest(e.message ?: "constraint violation")
    }

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
    val span = telemetry.startSpan("resource.write")
    try {
        span.setAttribute(AttributeKey.stringKey("kind"), kind)
        span.setAttribute(AttributeKey.stringKey("action"), action)
    } finally {
        span.end()
    }
}
