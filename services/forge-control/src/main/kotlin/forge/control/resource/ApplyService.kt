package forge.control.resource

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.AuditRepository
import forge.control.resource.http.ScopeCoords
import forge.control.telemetry.Telemetry
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import java.util.UUID

@Serializable
data class ApplyRequest(
    val dryRun: Boolean = false,
    val resources: List<ResourceWriteRequest> = emptyList(),
)

@Serializable
data class ApplyResponse(
    val operationId: String,
    val dryRun: Boolean,
    val changedCount: Int,
    val results: List<ApplyResourceResult>,
)

@Serializable
data class ApplyResourceResult(
    val kind: String,
    val name: String,
    val action: String,
    val project: String? = null,
    val environment: String? = null,
    val resource: ResourceEnvelopeResponse? = null,
    val message: String? = null,
)

/**
 * Server-side multi-resource apply with dry-run diff (step 20.07).
 * Validates the whole set before any mutation; parent-before-child by kind rank.
 */
class ApplyService(
    private val resources: ResourceRepository,
    private val kinds: KindRegistry,
    private val audit: AuditRepository? = null,
    private val actor: String = "dev",
    private val defaultOrganization: String = "default",
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    fun apply(request: ApplyRequest): ApplyResponse {
        val operationId = Ulid.next("apl")
        if (request.resources.isEmpty()) {
            throw ApiException.BadRequest("resources must not be empty", mapOf("field" to "resources"))
        }

        val planned = try {
            request.resources
                .mapIndexed { index, body -> planOne(index, body) }
                .sortedBy { KIND_ORDER[it.kind.kind] ?: 100 }
        } catch (e: ApiException) {
            telemetry.recordApplyOperation(request.dryRun, "rejected")
            throw e
        }

        if (request.dryRun) {
            val results = planned.map { p ->
                ApplyResourceResult(
                    kind = p.kind.kind,
                    name = p.name,
                    action = p.action,
                    project = p.scope.project,
                    environment = p.scope.environment,
                    message = p.message,
                )
            }
            val changed = results.count { it.action != "unchanged" }
            log?.info(
                "apply.operation",
                "operation_id" to operationId,
                "resource_count" to request.resources.size,
                "dry_run" to true,
                "changed_count" to changed,
            )
            telemetry.recordApplyOperation(dryRun = true, result = "ok")
            return ApplyResponse(operationId, dryRun = true, changedCount = changed, results = results)
        }

        val results = mutableListOf<ApplyResourceResult>()
        try {
            for (plan in planned) {
                val result = execute(plan)
                results += result
                if (result.action != "unchanged" && audit != null) {
                    val entityId = try {
                        UUID.fromString(result.resource?.metadata?.id ?: plan.existing?.id)
                    } catch (_: Exception) {
                        null
                    }
                    if (entityId != null && plan.kind.kind in CompatibilityResourceRepository.COMPAT_KINDS) {
                        audit.append(
                            entityType = plan.kind.kind.lowercase(),
                            entityId = entityId,
                            action = "apply_${result.action}",
                            actor = actor,
                            detailJson = """{"operationId":"$operationId","name":"${plan.name}"}""",
                        )
                    }
                }
            }
        } catch (e: ApiException) {
            telemetry.recordApplyOperation(dryRun = false, result = "error")
            throw e
        }

        val changed = results.count { it.action != "unchanged" }
        log?.info(
            "apply.operation",
            "operation_id" to operationId,
            "resource_count" to request.resources.size,
            "dry_run" to false,
            "changed_count" to changed,
        )
        telemetry.recordApplyOperation(dryRun = false, result = "ok")
        return ApplyResponse(operationId, dryRun = false, changedCount = changed, results = results)
    }

    private data class Planned(
        val index: Int,
        val kind: KindDescriptor,
        val name: String,
        val scope: ScopeCoords,
        val body: ResourceWriteRequest,
        val action: String,
        val existing: ResourceRow?,
        val message: String?,
    )

    private fun planOne(index: Int, body: ResourceWriteRequest): Planned {
        val kindName = body.kind?.trim().orEmpty()
        if (kindName.isEmpty()) {
            throw ApiException.BadRequest(
                "resources[$index].kind is required",
                mapOf("field" to "resources[$index].kind"),
            )
        }
        val kind = kinds.get(kindName)
            ?: throw ApiException.BadRequest(
                "kind not registered: $kindName",
                details = mapOf("kind" to kindName, "index" to index.toString()),
                code = "kind_not_registered",
            )
        val name = body.metadata?.name?.trim().orEmpty()
        if (name.isEmpty()) {
            throw ApiException.BadRequest(
                "resources[$index].metadata.name is required",
                mapOf("field" to "resources[$index].metadata.name"),
            )
        }
        PortableManifest.validate(kindName, body.spec)
        val organization = body.metadata?.organization?.trim().orEmpty().ifEmpty { defaultOrganization }
        val project = body.metadata?.project?.trim()?.takeIf { it.isNotEmpty() }
        val environment = body.metadata?.environment?.trim()?.takeIf { it.isNotEmpty() }
        validateScope(kind, project, environment, index)
        val scope = ScopeCoords(organization, project, environment)
        val existing = resources.findByScopeAndName(
            kind = kind.kind,
            organization = organization,
            project = project,
            environment = environment,
            name = name,
        )
        val (action, message) = when {
            existing == null -> "create" to "would create"
            GenerationPolicy.shouldBumpGeneration(existing.spec, body.spec) ||
                existing.labels != (body.metadata?.labels ?: existing.labels) ||
                existing.annotations != (body.metadata?.annotations ?: existing.annotations) ->
                "update" to "would update (spec or metadata changed)"
            else -> "unchanged" to "no changes"
        }
        // Stale resourceVersion on update → 409 before any mutation.
        if (existing != null && body.metadata?.resourceVersion != null) {
            val expected = body.metadata.resourceVersion.toLongOrNull()
                ?: throw ApiException.BadRequest(
                    "invalid metadata.resourceVersion",
                    mapOf("field" to "resources[$index].metadata.resourceVersion"),
                )
            ResourceVersionGuard.checkMatch(expected, existing.resourceVersion)
        }
        return Planned(index, kind, name, scope, body, action, existing, message)
    }

    private fun execute(plan: Planned): ApplyResourceResult {
        when (plan.action) {
            "unchanged" -> return ApplyResourceResult(
                kind = plan.kind.kind,
                name = plan.name,
                action = "unchanged",
                project = plan.scope.project,
                environment = plan.scope.environment,
                resource = plan.existing?.toResponse(),
                message = plan.message,
            )
            "create" -> {
                val created = resources.insert(
                    NewResourceRow(
                        id = if (plan.kind.kind in CompatibilityResourceRepository.COMPAT_KINDS) {
                            // Compatibility layer replaces with legacy UUID.
                            Ulid.next(plan.kind.idPrefix)
                        } else {
                            Ulid.next(plan.kind.idPrefix)
                        },
                        kind = plan.kind.kind,
                        apiVersion = plan.body.apiVersion?.trim().orEmpty().ifEmpty { "forge.dev/v1" },
                        organization = plan.scope.organization,
                        project = plan.scope.project,
                        environment = plan.scope.environment,
                        name = plan.name,
                        labels = plan.body.metadata?.labels ?: JsonObject(emptyMap()),
                        annotations = plan.body.metadata?.annotations ?: JsonObject(emptyMap()),
                        spec = plan.body.spec,
                        ownerRefs = plan.body.metadata?.ownerRefs ?: JsonArray(emptyList()),
                        finalizers = plan.body.metadata?.finalizers ?: JsonArray(emptyList()),
                    ),
                )
                return ApplyResourceResult(
                    kind = plan.kind.kind,
                    name = plan.name,
                    action = "create",
                    project = plan.scope.project,
                    environment = plan.scope.environment,
                    resource = created.toResponse(),
                )
            }
            "update" -> {
                val existing = plan.existing
                    ?: throw ApiException.NotFound("resource not found", code = "not_found")
                val expected = plan.body.metadata?.resourceVersion?.toLongOrNull()
                    ?: existing.resourceVersion
                val labels = plan.body.metadata?.labels ?: existing.labels
                val annotations = plan.body.metadata?.annotations ?: existing.annotations
                val bump = GenerationPolicy.shouldBumpGeneration(existing.spec, plan.body.spec)
                val updated = resources.replace(
                    id = existing.id,
                    expectedVersion = expected,
                    labels = labels,
                    annotations = annotations,
                    spec = plan.body.spec,
                    ownerRefs = plan.body.metadata?.ownerRefs ?: existing.ownerRefs,
                    finalizers = plan.body.metadata?.finalizers ?: existing.finalizers,
                    bumpGeneration = bump,
                )
                return ApplyResourceResult(
                    kind = plan.kind.kind,
                    name = plan.name,
                    action = "update",
                    project = plan.scope.project,
                    environment = plan.scope.environment,
                    resource = updated.toResponse(),
                )
            }
            else -> throw ApiException.BadRequest("unknown apply action ${plan.action}")
        }
    }

    private fun validateScope(
        kind: KindDescriptor,
        project: String?,
        environment: String?,
        index: Int,
    ) {
        when (kind.scope) {
            ResourceScope.Cluster -> {
                if (project != null || environment != null) {
                    // Allow but ignore — apply metadata may carry context.
                }
            }
            ResourceScope.Project -> {
                if (project.isNullOrBlank()) {
                    throw ApiException.BadRequest(
                        "resources[$index].metadata.project is required for ${kind.kind}",
                        mapOf("field" to "resources[$index].metadata.project"),
                    )
                }
            }
            ResourceScope.Environment -> {
                if (project.isNullOrBlank() || environment.isNullOrBlank()) {
                    throw ApiException.BadRequest(
                        "resources[$index].metadata.project and metadata.environment are required for ${kind.kind}",
                        mapOf("field" to "resources[$index].metadata"),
                    )
                }
            }
        }
    }

    companion object {
        private val KIND_ORDER = mapOf(
            "Organization" to 10,
            "Project" to 20,
            "Environment" to 30,
            "Application" to 40,
            "Service" to 50,
            "Deployment" to 60,
            "Revision" to 70,
            "Route" to 80,
            "Secret" to 90,
            "Config" to 100,
        )
    }
}
