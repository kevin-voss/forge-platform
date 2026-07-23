package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.http.idempotentCreate
import forge.control.logging.JsonLog
import forge.control.repo.IdempotencyStore
import forge.control.repo.RepositoryException
import forge.control.resource.CompatibilityResourceRepository
import forge.control.resource.CursorCodec
import forge.control.resource.Finalizers
import forge.control.resource.GenerationPolicy
import forge.control.resource.JsonPatch
import forge.control.resource.KindDescriptor
import forge.control.resource.KindRegistry
import forge.control.resource.LabelSelectorParser
import forge.control.resource.LabelValidator
import forge.control.resource.MergePatch
import forge.control.resource.NewResourceRow
import forge.control.resource.OwnerRefs
import forge.control.resource.ResourceEnvelopeResponse
import forge.control.resource.ResourceListQuery
import forge.control.resource.ResourceRepository
import forge.control.resource.ResourceRow
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
import io.ktor.server.request.header
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
    listDefaultPageSize: Int = 50,
    listMaxPageSize: Int = 200,
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
            listDefaultPageSize = listDefaultPageSize,
            listMaxPageSize = listMaxPageSize,
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
            listDefaultPageSize = listDefaultPageSize,
            listMaxPageSize = listMaxPageSize,
            log = log,
            telemetry = telemetry,
            resolveScope = { call, kind ->
                requireScope(kind, ResourceScope.Project)
                if (kind.parentKind != null) {
                    throw ApiException.NotFound(
                        "kind '${kind.kind}' requires parent path under ${kind.parentKind}",
                        details = mapOf("kind" to kind.kind, "parentKind" to kind.parentKind),
                        code = "kind_not_registered",
                    )
                }
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
                if (kind.parentKind != null) {
                    throw ApiException.NotFound(
                        "kind '${kind.kind}' requires parent path under ${kind.parentKind}",
                        details = mapOf("kind" to kind.kind, "parentKind" to kind.parentKind),
                        code = "kind_not_registered",
                    )
                }
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
            listDefaultPageSize = listDefaultPageSize,
            listMaxPageSize = listMaxPageSize,
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

    // Nested kinds with parentKind (Service under Application):
    // /v1/projects/{project}/applications/{application}/{plural}[/{name}]
    route("/v1/projects/{project}/applications/{application}/{plural}") {
        collectionRoutes(
            resources = resources,
            kinds = kinds,
            idempotency = idempotency,
            defaultOrganization = defaultOrganization,
            listDefaultPageSize = listDefaultPageSize,
            listMaxPageSize = listMaxPageSize,
            log = log,
            telemetry = telemetry,
            resolveScope = { call, kind ->
                if (kind.parentKind != "Application") {
                    throw ApiException.NotFound(
                        "kind '${kind.kind}' is not nested under Application",
                        details = mapOf("kind" to kind.kind),
                        code = "kind_not_registered",
                    )
                }
                requireScope(kind, ResourceScope.Project)
                ScopeCoords(
                    organization = defaultOrganization,
                    project = call.parameters["project"],
                    environment = null,
                    parentName = call.parameters["application"],
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
                if (kind.parentKind != "Application") {
                    throw ApiException.NotFound(
                        "kind '${kind.kind}' is not nested under Application",
                        details = mapOf("kind" to kind.kind),
                        code = "kind_not_registered",
                    )
                }
                requireScope(kind, ResourceScope.Project)
                ScopeCoords(
                    organization = defaultOrganization,
                    project = call.parameters["project"],
                    environment = null,
                    parentName = call.parameters["application"],
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
    listDefaultPageSize: Int,
    listMaxPageSize: Int,
    log: JsonLog?,
    telemetry: Telemetry,
    resolveScope: (ApplicationCall, KindDescriptor) -> ScopeCoords,
) {
    get {
        val kind = resolveKind(kinds, call.parameters["plural"])
        val scope = resolveScope(call, kind)
        val selectorRaw = call.request.queryParameters["labelSelector"]
        val selector = LabelSelectorParser.parse(selectorRaw)
        val phase = call.request.queryParameters["phase"]?.trim()?.takeIf { it.isNotEmpty() }
        val namePrefix = call.request.queryParameters["namePrefix"]
        val cursor = CursorCodec.decode(call.request.queryParameters["cursor"])
        val requestedLimit = call.request.queryParameters["limit"]?.toIntOrNull()
        val limit = resolveListLimit(
            requested = requestedLimit,
            defaultPageSize = listDefaultPageSize,
            maxPageSize = listMaxPageSize,
            log = log,
        )
        val result = resources.list(
            ResourceListQuery(
                kind = kind.kind,
                organization = scope.organization,
                project = scope.project,
                environment = scope.environment,
                selector = selector,
                phase = phase,
                namePrefix = namePrefix,
                limit = limit,
                cursor = cursor,
            ),
        )
        log?.info(
            "resource.list",
            "kind" to kind.kind,
            "project" to scope.project,
            "environment" to scope.environment,
            "selector_terms" to selector.terms.size,
            "result_count" to result.items.size,
            "has_more" to (result.nextCursor != null),
        )
        telemetry.recordResourceList(kind.kind, result.items.size)
        call.respond(
            ListEnvelope(
                apiVersion = "forge.dev/v1",
                kind = "${kind.kind}List",
                resourceVersion = result.resourceVersion.toString(),
                items = result.items.map { it.toResponse() },
                nextCursor = result.nextCursor,
            ),
        )
    }
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
            val raw = call.receiveText()
            rejectStatusOnSpecEndpoint(raw)
            val body = resourceJson.decodeFromString(ResourceWriteRequest.serializer(), raw)
            val existing = requireExisting(resources, kind, scope, name)
            val expected = parseResourceVersion(body.metadata?.resourceVersion)
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)
            val labels = body.metadata?.labels ?: existing.labels
            val annotations = body.metadata?.annotations ?: existing.annotations
            LabelValidator.validate(labels, annotations)
            val ownerRefs = body.metadata?.ownerRefs ?: existing.ownerRefs
            val finalizers = body.metadata?.finalizers ?: existing.finalizers
            validateOwnerRefs(resources, existing, ownerRefs)
            val bump = GenerationPolicy.shouldBumpGeneration(existing.spec, body.spec)
            val updated = try {
                resources.replace(
                    id = existing.id,
                    expectedVersion = expected,
                    labels = labels,
                    annotations = annotations,
                    spec = body.spec,
                    ownerRefs = ownerRefs,
                    finalizers = finalizers,
                    bumpGeneration = bump,
                )
            } catch (e: RepositoryException.Conflict) {
                throw mapRepo(e)
            }
            if (bump) {
                logGenerationBump(log, telemetry, kind.kind, name, existing.generation, updated.generation)
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
            rejectStatusOnSpecEndpoint(raw, contentType)
            val (labels, annotations, spec, expected) = applyPatch(
                existing = existing,
                contentType = contentType,
                raw = raw,
            )
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)
            LabelValidator.validate(labels, annotations)
            val bump = GenerationPolicy.shouldBumpGeneration(existing.spec, spec)
            val updated = resources.patch(
                id = existing.id,
                expectedVersion = expected,
                labels = labels,
                annotations = annotations,
                spec = spec,
                bumpGeneration = bump,
            )
            if (bump) {
                logGenerationBump(log, telemetry, kind.kind, name, existing.generation, updated.generation)
            }
            logWrite(log, telemetry, kind.kind, name, scope, "patch", expected, updated.resourceVersion)
            call.respond(updated.toResponse())
        }
        delete {
            val kind = resolveKind(kinds, call.parameters["plural"])
            val scope = resolveScope(call, kind)
            val name = call.parameters["name"]
                ?: throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            val existing = requireExisting(resources, kind, scope, name)
            enforceDeleteConfirmation(call, kind, name)
            applyCascadePolicy(
                resources = resources,
                kinds = kinds,
                parent = existing,
                cascade = call.request.queryParameters["cascade"]?.trim()?.lowercase(),
            )
            if (existing.deletionTimestamp != null) {
                // Idempotent re-DELETE while terminating.
                if (Finalizers.isEmpty(existing.finalizers)) {
                    val deleted = resources.softDelete(existing.id)
                    logWrite(
                        log, telemetry, kind.kind, name, scope,
                        "delete", existing.resourceVersion, deleted.resourceVersion,
                    )
                    call.respond(HttpStatusCode.NoContent)
                } else {
                    log?.info(
                        "resource.deletion_blocked",
                        "resource_id" to existing.id,
                        "kind" to kind.kind,
                        "finalizers" to Finalizers.asStrings(existing.finalizers).joinToString(","),
                    )
                    call.respond(existing.toResponse())
                }
                return@delete
            }
            if (Finalizers.isEmpty(existing.finalizers)) {
                val deleted = resources.softDelete(existing.id)
                logWrite(
                    log, telemetry, kind.kind, name, scope,
                    "delete", existing.resourceVersion, deleted.resourceVersion,
                )
                call.respond(HttpStatusCode.NoContent)
            } else {
                val terminating = resources.markTerminating(existing.id)
                log?.info(
                    "resource.deletion_blocked",
                    "resource_id" to terminating.id,
                    "kind" to kind.kind,
                    "finalizers" to Finalizers.asStrings(terminating.finalizers).joinToString(","),
                )
                logWrite(
                    log, telemetry, kind.kind, name, scope,
                    "terminate", existing.resourceVersion, terminating.resourceVersion,
                )
                call.respond(terminating.toResponse())
            }
        }
    }
}

internal data class ScopeCoords(
    val organization: String,
    val project: String?,
    val environment: String?,
    val parentName: String? = null,
)

private data class PatchResult(
    val labels: JsonObject,
    val annotations: JsonObject,
    val spec: JsonObject,
    val expectedVersion: Long,
)

internal fun resolveKind(kinds: KindRegistry, plural: String?): KindDescriptor {
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

internal fun requireScope(kind: KindDescriptor, expected: ResourceScope) {
    if (kind.scope != expected) {
        throw ApiException.NotFound(
            "kind '${kind.kind}' is not ${expected.name.lowercase()}-scoped",
            details = mapOf("kind" to kind.kind, "scope" to kind.scope.name),
            code = "kind_not_registered",
        )
    }
}

internal fun requireExisting(
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

/** Rejects a `status` field on the main resource endpoint (use `/status` instead). */
internal fun rejectStatusOnSpecEndpoint(raw: String, contentType: ContentType? = null) {
    val mime = contentType?.let { "${it.contentType}/${it.contentSubtype}".lowercase() }.orEmpty()
    if (mime == "application/json-patch+json") {
        val ops = resourceJson.parseToJsonElement(raw).jsonArray
        for (op in ops) {
            val path = op.jsonObject["path"]?.jsonPrimitive?.content.orEmpty()
            if (path == "/status" || path.startsWith("/status/")) {
                throw ApiException.BadRequest(
                    "status is read-only on this endpoint; use /status",
                    code = "spec_endpoint_status_forbidden",
                )
            }
        }
        return
    }
    val root = resourceJson.parseToJsonElement(raw)
    if (root is JsonObject && "status" in root) {
        throw ApiException.BadRequest(
            "status is read-only on this endpoint; use /status",
            code = "spec_endpoint_status_forbidden",
        )
    }
}

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
    val labels = body.metadata?.labels ?: JsonObject(emptyMap())
    val annotations = body.metadata?.annotations ?: JsonObject(emptyMap())
    LabelValidator.validate(labels, annotations)
    val ownerRefs = body.metadata?.ownerRefs ?: JsonArray(emptyList())
    val provisional = ResourceRow(
        id = Ulid.next(kind.idPrefix),
        kind = kind.kind,
        apiVersion = apiVersion,
        organization = organization,
        project = scope.project,
        environment = scope.environment,
        name = name,
        generation = 1,
        resourceVersion = 0,
        labels = labels,
        annotations = annotations,
        spec = body.spec,
        status = JsonObject(emptyMap()),
        ownerRefs = ownerRefs,
        finalizers = body.metadata?.finalizers ?: JsonArray(emptyList()),
        createdAt = java.time.Instant.EPOCH,
        updatedAt = java.time.Instant.EPOCH,
    )
    validateOwnerRefs(resources, provisional, ownerRefs)
    val effectiveAnnotations = if (!scope.parentName.isNullOrBlank()) {
        JsonObject(
            annotations + (
                CompatibilityResourceRepository.PARENT_ANNOTATION to JsonPrimitive(scope.parentName)
                ),
        )
    } else {
        annotations
    }
    return try {
        resources.insert(
            NewResourceRow(
                id = provisional.id,
                kind = kind.kind,
                apiVersion = apiVersion,
                organization = organization,
                project = scope.project,
                environment = scope.environment,
                name = name,
                labels = labels,
                annotations = effectiveAnnotations,
                spec = body.spec,
                ownerRefs = ownerRefs,
                finalizers = provisional.finalizers,
            ),
        )
    } catch (e: RepositoryException.Conflict) {
        throw ApiException.Conflict(e.message ?: "resource already exists", code = "conflict")
    } catch (e: RepositoryException.ConstraintViolation) {
        throw ApiException.BadRequest(e.message ?: "constraint violation")
    }
}

internal fun validateOwnerRefs(
    resources: ResourceRepository,
    subject: ResourceRow,
    ownerRefs: JsonArray,
) {
    if (ownerRefs.isEmpty()) return
    OwnerRefs.validate(subject, ownerRefs) { id -> resources.findById(id) }
}

internal fun enforceDeleteConfirmation(
    call: ApplicationCall,
    kind: KindDescriptor,
    name: String,
) {
    if (!kind.requiresDeleteConfirmation) return
    val confirmation = call.request.header("X-Forge-Delete-Confirmation")?.trim().orEmpty()
    if (confirmation != name) {
        throw ApiException.Conflict(
            "delete confirmation required: send X-Forge-Delete-Confirmation with the resource name",
            details = mapOf(
                "kind" to kind.kind,
                "name" to name,
                "header" to "X-Forge-Delete-Confirmation",
            ),
            code = "delete_confirmation_required",
        )
    }
}

/**
 * Default: reject when owned dependents exist (`owned_resources_exist`).
 * `cascade=orphan` clears child owner refs; `cascade=foreground` marks cascade-allowed
 * children Terminating (kinds with [KindDescriptor.allowsCascade]=false block).
 */
internal fun applyCascadePolicy(
    resources: ResourceRepository,
    kinds: KindRegistry,
    parent: ResourceRow,
    cascade: String?,
) {
    val children = resources.findOwnedBy(parent.id)
    if (children.isEmpty()) return
    when (cascade) {
        null, "" -> throw ApiException.Conflict(
            "resource has owned dependents; pass cascade=orphan or cascade=foreground",
            details = mapOf(
                "resourceId" to parent.id,
                "ownedCount" to children.size.toString(),
            ),
            code = "owned_resources_exist",
        )
        "orphan" -> resources.clearOwnerRefsTo(parent.id)
        "foreground" -> {
            val blocked = children.filter { child ->
                val childKind = kinds.get(child.kind)
                childKind == null || !childKind.allowsCascade
            }
            if (blocked.isNotEmpty()) {
                throw ApiException.Conflict(
                    "owned dependents do not allow cascade deletion",
                    details = mapOf(
                        "resourceId" to parent.id,
                        "blockedKinds" to blocked.map { it.kind }.distinct().joinToString(","),
                    ),
                    code = "owned_resources_exist",
                )
            }
            for (child in children) {
                if (child.deletionTimestamp == null) {
                    if (Finalizers.isEmpty(child.finalizers)) {
                        resources.softDelete(child.id)
                    } else {
                        resources.markTerminating(child.id)
                    }
                }
            }
        }
        else -> throw ApiException.BadRequest(
            "cascade must be orphan or foreground",
            details = mapOf("field" to "cascade"),
            code = "invalid_request",
        )
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

internal fun parseResourceVersion(raw: String?): Long {
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

private fun resolveListLimit(
    requested: Int?,
    defaultPageSize: Int,
    maxPageSize: Int,
    log: JsonLog?,
): Int {
    if (requested == null) return defaultPageSize.coerceIn(1, maxPageSize)
    if (requested < 1) {
        throw ApiException.BadRequest(
            "limit must be a positive integer",
            details = mapOf("field" to "limit"),
            code = "invalid_request",
        )
    }
    if (requested > maxPageSize) {
        log?.debug(
            "resource.list.limit_clamped",
            "requested" to requested,
            "max" to maxPageSize,
        )
        return maxPageSize
    }
    return requested
}

private fun logGenerationBump(
    log: JsonLog?,
    telemetry: Telemetry,
    kind: String,
    name: String,
    oldGeneration: Long,
    newGeneration: Long,
) {
    log?.info(
        "resource.generation_bump",
        "kind" to kind,
        "name" to name,
        "old_generation" to oldGeneration,
        "new_generation" to newGeneration,
    )
    telemetry.recordResourceGenerationBump(kind)
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
