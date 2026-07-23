package forge.control.resource

import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.AuditRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import forge.control.repo.ServiceRepository
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import java.util.UUID

/**
 * Compatibility facade (step 20.07): grandfathered kinds read/write legacy tables
 * and keep a companion envelope row in [control.resources] for the generic API.
 * New kinds (Organization, Revision, Route, Secret, Config, Widget, …) delegate
 * entirely to [jdbc].
 */
class CompatibilityResourceRepository(
    private val jdbc: ResourceRepository,
    private val projects: ProjectRepository,
    private val environments: EnvironmentRepository,
    private val applications: ApplicationRepository,
    private val services: ServiceRepository,
    private val deployments: DeploymentRepository,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) : ResourceRepository {

    override fun insert(row: NewResourceRow): ResourceRow =
        when (row.kind) {
            "Project" -> insertProject(row)
            "Environment" -> insertEnvironment(row)
            "Application" -> insertApplication(row)
            "Service" -> insertService(row)
            "Deployment" -> insertDeployment(row)
            else -> jdbc.insert(row)
        }

    override fun findById(id: String): ResourceRow? = jdbc.findById(id)

    override fun findByScopeAndName(
        kind: String,
        organization: String,
        project: String?,
        environment: String?,
        name: String,
    ): ResourceRow? {
        val existing = jdbc.findByScopeAndName(kind, organization, project, environment, name)
        if (existing != null) return existing
        return when (kind) {
            "Application" -> ensureApplicationCompanion(organization, project, environment, name)
            "Project" -> ensureProjectCompanion(organization, name)
            "Environment" -> ensureEnvironmentCompanion(organization, project, name)
            else -> null
        }
    }

    override fun replace(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        ownerRefs: JsonArray,
        finalizers: JsonArray,
        bumpGeneration: Boolean,
    ): ResourceRow {
        val current = jdbc.findById(id)
            ?: throw ApiException.NotFound("resource not found", details = mapOf("id" to id), code = "not_found")
        if (current.kind in COMPAT_KINDS) {
            PortableManifest.validate(current.kind, spec)
            syncLegacyOnUpdate(current, spec)
        }
        return jdbc.replace(
            id, expectedVersion, labels, annotations, spec, ownerRefs, finalizers, bumpGeneration,
        )
    }

    override fun patch(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        bumpGeneration: Boolean,
    ): ResourceRow {
        val current = jdbc.findById(id)
            ?: throw ApiException.NotFound("resource not found", details = mapOf("id" to id), code = "not_found")
        if (current.kind in COMPAT_KINDS) {
            PortableManifest.validate(current.kind, spec)
            syncLegacyOnUpdate(current, spec)
        }
        return jdbc.patch(id, expectedVersion, labels, annotations, spec, bumpGeneration)
    }

    override fun updateStatus(id: String, expectedVersion: Long, status: JsonObject): ResourceRow =
        jdbc.updateStatus(id, expectedVersion, status)

    override fun softDelete(id: String): ResourceRow {
        val current = jdbc.findById(id)
        val deleted = jdbc.softDelete(id)
        if (current != null && current.kind in COMPAT_KINDS) {
            softDeleteLegacy(current)
        }
        return deleted
    }

    override fun markTerminating(id: String): ResourceRow = jdbc.markTerminating(id)

    override fun replaceFinalizers(id: String, finalizers: JsonArray): ResourceRow =
        jdbc.replaceFinalizers(id, finalizers)

    override fun findOwnedBy(ownerId: String): List<ResourceRow> = jdbc.findOwnedBy(ownerId)

    override fun clearOwnerRefsTo(ownerId: String): Int = jdbc.clearOwnerRefsTo(ownerId)

    override fun list(query: ResourceListQuery): ResourceListResult = jdbc.list(query)

    private fun insertProject(row: NewResourceRow): ResourceRow {
        PortableManifest.validate("Project", row.spec)
        val slug = row.spec["slug"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
            .ifEmpty { row.name }
        val created = try {
            projects.create(row.name, slug)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "project already exists",
                details = mapOf("name" to row.name, "slug" to slug),
                code = "conflict",
            )
        }
        audit.append("project", created.id, "create", actor, """{"name":"${row.name}","slug":"$slug","via":"resource"}""")
        return jdbc.insert(
            row.copy(
                id = created.id.toString(),
                project = null,
                environment = null,
                spec = JsonObject(
                    row.spec + ("slug" to JsonPrimitive(slug)),
                ),
            ),
        )
    }

    private fun insertEnvironment(row: NewResourceRow): ResourceRow {
        PortableManifest.validate("Environment", row.spec)
        val projectKey = row.project?.trim().orEmpty()
        if (projectKey.isEmpty()) {
            throw ApiException.BadRequest("project is required", mapOf("field" to "metadata.project"))
        }
        val project = resolveProject(projectKey)
        val created = try {
            environments.create(project.id, row.name)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "environment already exists",
                details = mapOf("name" to row.name, "project" to project.slug),
                code = "conflict",
            )
        }
        audit.append(
            "environment",
            created.id,
            "create",
            actor,
            """{"projectId":"${project.id}","name":"${row.name}","via":"resource"}""",
        )
        return jdbc.insert(
            row.copy(
                id = created.id.toString(),
                project = project.slug,
                environment = null,
            ),
        )
    }

    private fun insertApplication(row: NewResourceRow): ResourceRow {
        PortableManifest.validate("Application", row.spec)
        val projectKey = row.project?.trim().orEmpty()
        if (projectKey.isEmpty()) {
            throw ApiException.BadRequest("project is required", mapOf("field" to "metadata.project"))
        }
        if (row.environment.isNullOrBlank()) {
            throw ApiException.BadRequest("environment is required", mapOf("field" to "metadata.environment"))
        }
        val project = resolveProject(projectKey)
        // Ensure the addressable environment exists (legacy env table).
        environments.findByProjectAndName(project.id, row.environment)
            ?: environments.create(project.id, row.environment)
        val existing = applications.findByProjectAndName(project.id, row.name)
        val app = existing ?: try {
            applications.create(project.id, row.name).also { created ->
                audit.append(
                    "application",
                    created.id,
                    "create",
                    actor,
                    """{"projectId":"${project.id}","name":"${row.name}","via":"resource"}""",
                )
            }
        } catch (e: RepositoryException.Conflict) {
            applications.findByProjectAndName(project.id, row.name)
                ?: throw ApiException.Conflict(
                    "application already exists",
                    details = mapOf("name" to row.name, "project" to project.slug),
                    code = "conflict",
                )
        }
        val companion = jdbc.findById(app.id.toString())
        if (companion != null && companion.deletedAt == null) {
            throw ApiException.Conflict(
                "application resource already exists",
                details = mapOf("name" to row.name, "project" to project.slug, "environment" to row.environment),
                code = "conflict",
            )
        }
        return jdbc.insert(
            row.copy(
                id = app.id.toString(),
                project = project.slug,
                environment = row.environment,
            ),
        )
    }

    private fun insertService(row: NewResourceRow): ResourceRow {
        PortableManifest.validate("Service", row.spec)
        val projectKey = row.project?.trim().orEmpty()
        if (projectKey.isEmpty()) {
            throw ApiException.BadRequest("project is required", mapOf("field" to "metadata.project"))
        }
        val parentName = row.annotations[PARENT_ANNOTATION]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
        if (parentName.isEmpty()) {
            throw ApiException.BadRequest(
                "Service requires parent Application ($PARENT_ANNOTATION annotation)",
                mapOf("field" to "metadata.annotations.$PARENT_ANNOTATION"),
            )
        }
        val project = resolveProject(projectKey)
        val application = applications.findByProjectAndName(project.id, parentName)
            ?: throw ApiException.NotFound(
                "parent application not found",
                details = mapOf("application" to parentName, "project" to project.slug),
                code = "not_found",
            )
        val port = row.spec["port"]?.jsonPrimitive?.contentOrNull?.toIntOrNull()
            ?: throw ApiException.BadRequest("spec.port is required", mapOf("field" to "spec.port"))
        val created = try {
            services.create(application.id, row.name, port)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "service already exists",
                details = mapOf("name" to row.name, "application" to parentName),
                code = "conflict",
            )
        }
        audit.append(
            "service",
            created.id,
            "create",
            actor,
            """{"applicationId":"${application.id}","name":"${row.name}","port":$port,"via":"resource"}""",
        )
        return jdbc.insert(
            row.copy(
                id = created.id.toString(),
                project = project.slug,
                annotations = JsonObject(
                    row.annotations + (PARENT_ANNOTATION to JsonPrimitive(parentName)),
                ),
            ),
        )
    }

    private fun insertDeployment(row: NewResourceRow): ResourceRow {
        PortableManifest.validate("Deployment", row.spec)
        val projectKey = row.project?.trim().orEmpty()
        val environmentName = row.environment?.trim().orEmpty()
        if (projectKey.isEmpty() || environmentName.isEmpty()) {
            throw ApiException.BadRequest(
                "project and environment are required",
                mapOf("field" to "metadata.project"),
            )
        }
        val project = resolveProject(projectKey)
        val environment = environments.findByProjectAndName(project.id, environmentName)
            ?: throw ApiException.NotFound(
                "environment not found",
                details = mapOf("environment" to environmentName, "project" to project.slug),
                code = "not_found",
            )
        val serviceName = row.spec["service"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
            .ifEmpty { row.name }
        val applicationName = row.annotations[PARENT_ANNOTATION]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
        val service = if (applicationName.isNotEmpty()) {
            val app = applications.findByProjectAndName(project.id, applicationName)
                ?: throw ApiException.NotFound(
                    "application not found",
                    details = mapOf("application" to applicationName),
                    code = "not_found",
                )
            services.findByApplicationAndName(app.id, serviceName)
        } else {
            applications.list(project.id).asSequence()
                .mapNotNull { app -> services.findByApplicationAndName(app.id, serviceName) }
                .firstOrNull()
        } ?: throw ApiException.NotFound(
            "service not found",
            details = mapOf("service" to serviceName, "project" to project.slug),
            code = "not_found",
        )
        val image = row.spec["image"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
        if (image.isEmpty()) {
            throw ApiException.BadRequest("spec.image is required", mapOf("field" to "spec.image"))
        }
        val replicas = row.spec["replicas"]?.jsonPrimitive?.contentOrNull?.toIntOrNull() ?: 1
        val created = try {
            deployments.create(
                serviceId = service.id,
                environmentId = environment.id,
                image = image,
                desiredReplicas = replicas,
                name = row.name,
            )
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "deployment already exists",
                details = mapOf("name" to row.name, "environment" to environmentName),
                code = "conflict",
            )
        }
        audit.append(
            "deployment",
            created.id,
            "create",
            actor,
            """{"serviceId":"${service.id}","environmentId":"${environment.id}","name":"${row.name}","via":"resource"}""",
        )
        return jdbc.insert(
            row.copy(
                id = created.id.toString(),
                project = project.slug,
                environment = environmentName,
            ),
        )
    }

    private fun syncLegacyOnUpdate(current: ResourceRow, spec: JsonObject) {
        when (current.kind) {
            "Deployment" -> {
                val id = parseUuid(current.id) ?: return
                val image = spec["image"]?.jsonPrimitive?.contentOrNull
                val replicas = spec["replicas"]?.jsonPrimitive?.contentOrNull?.toIntOrNull()
                if (image != null || replicas != null) {
                    deployments.update(id, image = image, desiredReplicas = replicas)
                }
            }
            "Service" -> {
                val id = parseUuid(current.id) ?: return
                val port = spec["port"]?.jsonPrimitive?.contentOrNull?.toIntOrNull()
                if (port != null) {
                    services.update(id, name = null, port = port)
                }
            }
            else -> Unit
        }
    }

    private fun softDeleteLegacy(current: ResourceRow) {
        val id = parseUuid(current.id) ?: return
        try {
            when (current.kind) {
                "Application" -> applications.delete(id)
                "Service" -> services.delete(id)
                "Deployment" -> deployments.delete(id)
                "Environment" -> environments.delete(id)
                "Project" -> projects.delete(id)
            }
        } catch (_: RepositoryException.NotFound) {
            // Companion already authoritative for delete visibility.
        } catch (_: RepositoryException.ConstraintViolation) {
            // Leave legacy row if FK children remain; companion is soft-deleted.
        }
    }

    private fun ensureApplicationCompanion(
        organization: String,
        projectKey: String?,
        environment: String?,
        name: String,
    ): ResourceRow? {
        if (projectKey.isNullOrBlank() || environment.isNullOrBlank()) return null
        val project = resolveProjectOrNull(projectKey) ?: return null
        val app = applications.findByProjectAndName(project.id, name) ?: return null
        environments.findByProjectAndName(project.id, environment) ?: return null
        jdbc.findById(app.id.toString())?.let { return it }
        return jdbc.insert(
            NewResourceRow(
                id = app.id.toString(),
                kind = "Application",
                organization = organization,
                project = project.slug,
                environment = environment,
                name = name,
                spec = JsonObject(emptyMap()),
            ),
        )
    }

    private fun ensureProjectCompanion(organization: String, name: String): ResourceRow? {
        val project = resolveProjectOrNull(name) ?: return null
        jdbc.findById(project.id.toString())?.let { return it }
        return jdbc.insert(
            NewResourceRow(
                id = project.id.toString(),
                kind = "Project",
                organization = organization,
                project = null,
                environment = null,
                name = project.slug,
                spec = JsonObject(mapOf("slug" to JsonPrimitive(project.slug))),
            ),
        )
    }

    private fun ensureEnvironmentCompanion(
        organization: String,
        projectKey: String?,
        name: String,
    ): ResourceRow? {
        if (projectKey.isNullOrBlank()) return null
        val project = resolveProjectOrNull(projectKey) ?: return null
        val env = environments.findByProjectAndName(project.id, name) ?: return null
        jdbc.findById(env.id.toString())?.let { return it }
        return jdbc.insert(
            NewResourceRow(
                id = env.id.toString(),
                kind = "Environment",
                organization = organization,
                project = project.slug,
                environment = null,
                name = name,
                spec = JsonObject(emptyMap()),
            ),
        )
    }

    private fun resolveProject(key: String) =
        resolveProjectOrNull(key)
            ?: throw ApiException.NotFound(
                "project not found",
                details = mapOf("project" to key),
                code = "not_found",
            )

    private fun resolveProjectOrNull(key: String) =
        parseUuid(key)?.let { projects.findById(it) } ?: projects.findBySlug(key)

    private fun parseUuid(raw: String): UUID? =
        try {
            UUID.fromString(raw)
        } catch (_: IllegalArgumentException) {
            null
        }

    companion object {
        const val PARENT_ANNOTATION = "forge.dev/parent"

        val COMPAT_KINDS = setOf(
            "Project",
            "Environment",
            "Application",
            "Service",
            "Deployment",
        )
    }
}
