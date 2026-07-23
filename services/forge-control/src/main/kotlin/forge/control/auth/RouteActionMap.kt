package forge.control.auth

import forge.control.manageddb.ManagedDbRepository
import forge.control.repo.ApplicationRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ServiceRepository
import java.util.UUID

/** Resolved authorization target for a Control HTTP request. */
sealed class AuthTarget {
    /** Health / internal platform routes — middleware skips auth. */
    data object Skip : AuthTarget()

    /** Authenticate only (active token); no project-scoped authz check. */
    data object AuthenticateOnly : AuthTarget()

    /** Authenticate + authorize [action] on [projectId]. */
    data class Authorize(
        val action: String,
        val projectId: String,
    ) : AuthTarget()
}

/**
 * Resolves Control resource IDs to the owning project id for authz.
 */
fun interface ProjectScopeResolver {
    fun resolve(kind: ScopeKind, id: String): String?
}

enum class ScopeKind {
    Project,
    Environment,
    Application,
    Service,
    Deployment,
    DbInstance,
    DbDatabase,
    DbAttachment,
    DbBackup,
}

class RepositoryProjectScopeResolver(
    private val applications: ApplicationRepository,
    private val services: ServiceRepository,
    private val environments: EnvironmentRepository,
    private val deployments: DeploymentRepository,
    private val dbInstances: ManagedDbRepository? = null,
) : ProjectScopeResolver {
    override fun resolve(kind: ScopeKind, id: String): String? {
        val uuid = id.toUuidOrNull() ?: return if (kind == ScopeKind.Project) id else null
        return when (kind) {
            ScopeKind.Project -> uuid.toString()
            ScopeKind.Environment -> environments.findById(uuid)?.projectId?.toString()
            ScopeKind.Application -> applications.findById(uuid)?.projectId?.toString()
            ScopeKind.Service -> {
                val service = services.findById(uuid) ?: return null
                applications.findById(service.applicationId)?.projectId?.toString()
            }
            ScopeKind.Deployment -> {
                val deployment = deployments.findById(uuid) ?: return null
                val service = services.findById(deployment.serviceId) ?: return null
                applications.findById(service.applicationId)?.projectId?.toString()
            }
            ScopeKind.DbInstance -> dbInstances?.findInstanceById(uuid)?.projectId?.toString()
            ScopeKind.DbDatabase -> {
                val database = dbInstances?.findDatabaseById(uuid) ?: return null
                dbInstances.findInstanceById(database.instanceId)?.projectId?.toString()
            }
            ScopeKind.DbAttachment -> {
                val attachment = dbInstances?.findAttachmentById(uuid) ?: return null
                val database = dbInstances.findDatabaseById(attachment.databaseId) ?: return null
                dbInstances.findInstanceById(database.instanceId)?.projectId?.toString()
            }
            ScopeKind.DbBackup -> {
                val backup = dbInstances?.findBackupById(uuid) ?: return null
                val database = dbInstances.findDatabaseById(backup.databaseId) ?: return null
                dbInstances.findInstanceById(database.instanceId)?.projectId?.toString()
            }
        }
    }
}

/** Fixed resolver for unit tests. */
class MapProjectScopeResolver(
    private val byKind: Map<ScopeKind, Map<String, String>> = emptyMap(),
) : ProjectScopeResolver {
    override fun resolve(kind: ScopeKind, id: String): String? = byKind[kind]?.get(id)
}

/**
 * Maps (method, path) → auth target using Control's public API surface.
 *
 * Internal fleet/placement/status routes are skipped so Runtime can operate until
 * service-account wiring lands; pre-09 demos continue with FORGE_AUTH_MODE=dev.
 */
class RouteActionMap(
    private val scope: ProjectScopeResolver = MapProjectScopeResolver(),
) {
    fun resolve(method: String, path: String): AuthTarget {
        val m = method.uppercase()
        val p = path.trimEnd('/').ifEmpty { "/" }

        if (p.startsWith("/health")) return AuthTarget.Skip

        // Operator-issued bootstrap tokens require authentication (dev mode still bypasses).
        if (p.startsWith("/v1/nodes/bootstrap-tokens")) return AuthTarget.AuthenticateOnly
        // Platform-internal (Runtime / scheduler / kind registration) — not product-tenant mutations.
        if (p.startsWith("/v1/nodes") || p.startsWith("/v1/placements")) return AuthTarget.Skip
        if (p.startsWith("/v1/priority-classes") || p.startsWith("/v1/preemption-events")) {
            return AuthTarget.Skip
        }
        if (p.startsWith("/v1/kinds")) return AuthTarget.Skip
        if (m == "POST" && DEPLOYMENT_STATUS.matches(p)) return AuthTarget.Skip

        match(m, p, PROJECTS_COLLECTION)?.let {
            return when (m) {
                "GET" -> AuthTarget.AuthenticateOnly
                "POST" -> AuthTarget.AuthenticateOnly // org-scoped create; membership checked later
                else -> AuthTarget.AuthenticateOnly
            }
        }

        match(m, p, PROJECT_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("project.read", ScopeKind.Project, id)
                "DELETE" -> authorize("project.delete", ScopeKind.Project, id)
                "PATCH", "PUT" -> authorize("project.write", ScopeKind.Project, id)
                else -> authorize("project.read", ScopeKind.Project, id)
            }
        }

        match(m, p, PROJECT_ENVIRONMENTS)?.let { projectId ->
            return when (m) {
                "GET" -> authorize("environment.read", ScopeKind.Project, projectId)
                "POST" -> authorize("environment.write", ScopeKind.Project, projectId)
                else -> authorize("environment.read", ScopeKind.Project, projectId)
            }
        }

        match(m, p, ENVIRONMENT_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("environment.read", ScopeKind.Environment, id)
                else -> authorize("environment.write", ScopeKind.Environment, id)
            }
        }

        match(m, p, PROJECT_APPLICATIONS)?.let { projectId ->
            return when (m) {
                "GET" -> authorize("application.read", ScopeKind.Project, projectId)
                "POST" -> authorize("application.write", ScopeKind.Project, projectId)
                else -> authorize("application.read", ScopeKind.Project, projectId)
            }
        }

        match(m, p, APPLICATION_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("application.read", ScopeKind.Application, id)
                else -> authorize("application.write", ScopeKind.Application, id)
            }
        }

        match(m, p, APPLICATION_SERVICES)?.let { id ->
            return when (m) {
                "GET" -> authorize("service.read", ScopeKind.Application, id)
                "POST" -> authorize("service.write", ScopeKind.Application, id)
                else -> authorize("service.read", ScopeKind.Application, id)
            }
        }

        match(m, p, SERVICE_IMAGE)?.let { id ->
            return authorize("service.write", ScopeKind.Service, id)
        }

        match(m, p, SERVICE_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("service.read", ScopeKind.Service, id)
                else -> authorize("service.write", ScopeKind.Service, id)
            }
        }

        match(m, p, SERVICE_DEPLOYMENTS)?.let { id ->
            return when (m) {
                "GET" -> authorize("deployment.read", ScopeKind.Service, id)
                "POST" -> authorize("deployment.create", ScopeKind.Service, id)
                else -> authorize("deployment.read", ScopeKind.Service, id)
            }
        }

        match(m, p, DEPLOYMENT_HISTORY)?.let { id ->
            return authorize("deployment.read", ScopeKind.Deployment, id)
        }

        match(m, p, DEPLOYMENT_RECONCILE)?.let { id ->
            return authorize("deployment.read", ScopeKind.Deployment, id)
        }

        match(m, p, DEPLOYMENT_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("deployment.read", ScopeKind.Deployment, id)
                "PATCH" -> authorize("deployment.update", ScopeKind.Deployment, id)
                "DELETE" -> authorize("deployment.delete", ScopeKind.Deployment, id)
                else -> authorize("deployment.read", ScopeKind.Deployment, id)
            }
        }

        // Managed DB collection: project comes from body/query/header — authenticate only here.
        match(m, p, DB_INSTANCES)?.let {
            return when (m) {
                "GET" -> AuthTarget.AuthenticateOnly
                "POST" -> AuthTarget.AuthenticateOnly
                else -> AuthTarget.AuthenticateOnly
            }
        }

        match(m, p, DB_INSTANCE_DATABASES)?.let { id ->
            return when (m) {
                "GET" -> authorize("database.read", ScopeKind.DbInstance, id)
                "POST" -> authorize("database.write", ScopeKind.DbInstance, id)
                else -> authorize("database.read", ScopeKind.DbInstance, id)
            }
        }

        match(m, p, DB_INSTANCE_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("database.read", ScopeKind.DbInstance, id)
                "POST", "PATCH", "DELETE" -> authorize("database.write", ScopeKind.DbInstance, id)
                else -> authorize("database.read", ScopeKind.DbInstance, id)
            }
        }

        match(m, p, DB_DATABASE_ROTATE)?.let { id ->
            return authorize("database.write", ScopeKind.DbDatabase, id)
        }

        match(m, p, DB_DATABASE_ATTACH)?.let { id ->
            return when (m) {
                "POST" -> authorize("database.write", ScopeKind.DbDatabase, id)
                else -> authorize("database.write", ScopeKind.DbDatabase, id)
            }
        }

        match(m, p, DB_DATABASE_BACKUPS)?.let { id ->
            return when (m) {
                "GET" -> authorize("database.read", ScopeKind.DbDatabase, id)
                "POST" -> authorize("database.write", ScopeKind.DbDatabase, id)
                else -> authorize("database.read", ScopeKind.DbDatabase, id)
            }
        }

        match(m, p, DB_DATABASE_BACKUP_ITEM)?.let { id ->
            return authorize("database.read", ScopeKind.DbDatabase, id)
        }

        match(m, p, DB_BACKUP_RESTORE)?.let { id ->
            return authorize("database.write", ScopeKind.DbBackup, id)
        }

        match(m, p, DB_ATTACHMENT_ITEM)?.let { id ->
            return when (m) {
                "DELETE" -> authorize("database.write", ScopeKind.DbAttachment, id)
                "GET" -> authorize("database.read", ScopeKind.DbAttachment, id)
                else -> authorize("database.write", ScopeKind.DbAttachment, id)
            }
        }

        match(m, p, APPLICATION_DATABASES)?.let { id ->
            return when (m) {
                "GET" -> authorize("application.read", ScopeKind.Application, id)
                else -> authorize("application.read", ScopeKind.Application, id)
            }
        }

        match(m, p, DB_DATABASE_ITEM)?.let { id ->
            return when (m) {
                "GET" -> authorize("database.read", ScopeKind.DbDatabase, id)
                "POST", "PATCH", "DELETE" -> authorize("database.write", ScopeKind.DbDatabase, id)
                else -> authorize("database.read", ScopeKind.DbDatabase, id)
            }
        }

        // Unknown /v1 routes: require authentication (fail closed on anonymity).
        if (p.startsWith("/v1/")) return AuthTarget.AuthenticateOnly
        return AuthTarget.Skip
    }

    private fun authorize(action: String, kind: ScopeKind, id: String): AuthTarget {
        val projectId = scope.resolve(kind, id)
            ?: return AuthTarget.AuthenticateOnly
        return AuthTarget.Authorize(action = action, projectId = projectId)
    }

    /** Returns the first capture group, or empty string for collection matches. */
    private fun match(method: String, path: String, pattern: Pattern): String? {
        if (pattern.methods.isNotEmpty() && method !in pattern.methods) return null
        val result = pattern.regex.matchEntire(path) ?: return null
        return if (result.groups.size > 1) {
            result.groups[1]?.value.orEmpty()
        } else {
            ""
        }
    }

    private data class Pattern(
        val regex: Regex,
        val methods: Set<String> = emptySet(),
    )

    companion object {
        private val UUID_OR_ID = "[^/]+"

        private val PROJECTS_COLLECTION = Pattern(Regex("^/v1/projects$"))
        private val PROJECT_ITEM = Pattern(Regex("^/v1/projects/($UUID_OR_ID)$"))
        private val PROJECT_ENVIRONMENTS = Pattern(Regex("^/v1/projects/($UUID_OR_ID)/environments$"))
        private val ENVIRONMENT_ITEM = Pattern(Regex("^/v1/environments/($UUID_OR_ID)$"))
        private val PROJECT_APPLICATIONS = Pattern(Regex("^/v1/projects/($UUID_OR_ID)/applications$"))
        private val APPLICATION_ITEM = Pattern(Regex("^/v1/applications/($UUID_OR_ID)$"))
        private val APPLICATION_SERVICES = Pattern(Regex("^/v1/applications/($UUID_OR_ID)/services$"))
        private val SERVICE_ITEM = Pattern(Regex("^/v1/services/($UUID_OR_ID)$"))
        private val SERVICE_IMAGE = Pattern(Regex("^/v1/services/($UUID_OR_ID)/image$"))
        private val SERVICE_DEPLOYMENTS = Pattern(Regex("^/v1/services/($UUID_OR_ID)/deployments$"))
        private val DEPLOYMENT_ITEM = Pattern(Regex("^/v1/deployments/($UUID_OR_ID)$"))
        private val DEPLOYMENT_STATUS = Regex("^/v1/deployments/$UUID_OR_ID/status$")
        private val DEPLOYMENT_HISTORY = Pattern(Regex("^/v1/deployments/($UUID_OR_ID)/history$"))
        private val DEPLOYMENT_RECONCILE = Pattern(Regex("^/v1/deployments/($UUID_OR_ID)/reconcile$"))
        private val DB_INSTANCES = Pattern(Regex("^/v1/databases/instances$"))
        private val DB_INSTANCE_ITEM = Pattern(Regex("^/v1/databases/instances/($UUID_OR_ID)$"))
        private val DB_INSTANCE_DATABASES =
            Pattern(Regex("^/v1/databases/instances/($UUID_OR_ID)/databases$"))
        // Exclude the "instances" / "attachments" collection segments from bare database ids.
        private val DB_DATABASE_ITEM =
            Pattern(
                Regex(
                    "^/v1/databases/(?!instances(?:/|$)|attachments(?:/|$)|backups(?:/|$))" +
                        "($UUID_OR_ID)$",
                ),
            )
        private val DB_DATABASE_ROTATE =
            Pattern(
                Regex(
                    "^/v1/databases/(?!instances(?:/|$)|attachments(?:/|$)|backups(?:/|$))" +
                        "($UUID_OR_ID)/rotate-credentials$",
                ),
            )
        private val DB_DATABASE_ATTACH =
            Pattern(
                Regex(
                    "^/v1/databases/(?!instances(?:/|$)|attachments(?:/|$)|backups(?:/|$))" +
                        "($UUID_OR_ID)/attach$",
                ),
            )
        private val DB_DATABASE_BACKUPS =
            Pattern(
                Regex(
                    "^/v1/databases/(?!instances(?:/|$)|attachments(?:/|$)|backups(?:/|$))" +
                        "($UUID_OR_ID)/backups$",
                ),
            )
        private val DB_DATABASE_BACKUP_ITEM =
            Pattern(
                Regex(
                    "^/v1/databases/(?!instances(?:/|$)|attachments(?:/|$)|backups(?:/|$))" +
                        "($UUID_OR_ID)/backups/$UUID_OR_ID$",
                ),
            )
        private val DB_BACKUP_RESTORE =
            Pattern(Regex("^/v1/databases/backups/($UUID_OR_ID)/restore$"))
        private val DB_ATTACHMENT_ITEM =
            Pattern(Regex("^/v1/databases/attachments/($UUID_OR_ID)$"))
        private val APPLICATION_DATABASES =
            Pattern(Regex("^/v1/applications/($UUID_OR_ID)/databases$"))
    }
}

private fun String.toUuidOrNull(): UUID? =
    try {
        UUID.fromString(this)
    } catch (_: IllegalArgumentException) {
        null
    }
