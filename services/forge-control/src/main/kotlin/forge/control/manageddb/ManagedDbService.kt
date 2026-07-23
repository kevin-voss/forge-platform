package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.RepositoryException
import forge.control.service.ProjectService
import forge.control.service.RelationshipValidator
import forge.control.telemetry.Telemetry
import java.util.UUID

data class CreatedDatabase(
    val database: DbDatabase,
    val instance: DbInstance,
    val credential: DbCredential,
    /** One-time plaintext password for the create response only. */
    val password: String,
)

class ManagedDbService(
    private val store: ManagedDbRepository,
    private val provisioner: Provisioner,
    private val isolation: IsolationGuard,
    private val relationships: RelationshipValidator,
    private val secrets: ManagedDbSecretsClient,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    fun createInstance(projectId: UUID, nameRaw: String?): DbInstance {
        relationships.requireProject(projectId)
        val name = ProjectService.validateName(nameRaw)
        val created = try {
            store.createInstance(projectId, name, status = DbInstanceStatus.Provisioning)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "database instance name already exists in project",
                mapOf("name" to name, "projectId" to projectId.toString()),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            relationships.requireProject(projectId)
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        telemetry.recordManagedDbInstance(DbInstanceStatus.Provisioning.wire)
        log?.info(
            "managed db instance created",
            "instance_id" to created.id,
            "project_id" to projectId,
            "name" to name,
            "status" to created.status.wire,
        )

        val started = System.nanoTime()
        return try {
            val result = provisioner.createInstance(created.id, projectId, name)
            isolation.assertNotControlDatabase(result.endpointRef)
            rememberLocalEndpoint(created.id, result)
            transition(
                created,
                DbInstanceStatus.Available,
                endpointRef = result.endpointRef,
                host = result.host,
                port = result.port,
                containerId = result.containerId,
            ).also {
                telemetry.recordManagedDbProvisionDuration(
                    (System.nanoTime() - started) / 1_000_000_000.0,
                    "instance",
                )
            }
        } catch (e: IsolationViolation) {
            telemetry.recordManagedDbProvisionError("instance")
            transition(created, DbInstanceStatus.Error, reason = e.message)
            throw ApiException.BadRequest(
                e.message ?: "isolation invariant violated",
                mapOf("instanceId" to created.id.toString()),
            )
        } catch (e: Exception) {
            telemetry.recordManagedDbProvisionError("instance")
            val reason = e.message ?: e.javaClass.simpleName
            transition(created, DbInstanceStatus.Error, reason = reason)
            log?.error(
                "managed db provisioner failed",
                "instance_id" to created.id,
                "error" to reason,
            )
            store.findInstanceById(created.id)
                ?: throw ApiException.NotFound(
                    "database instance not found",
                    mapOf("id" to created.id.toString()),
                )
        }
    }

    fun getInstance(id: UUID): DbInstance =
        store.findInstanceById(id)
            ?: throw ApiException.NotFound(
                "database instance not found",
                mapOf("id" to id.toString()),
            )

    fun listInstances(projectId: UUID): List<DbInstance> {
        relationships.requireProject(projectId)
        return store.listInstances(projectId)
    }

    fun listDatabases(instanceId: UUID): List<DbDatabaseResponse> {
        val instance = getInstance(instanceId)
        return store.listDatabases(instanceId).map { db ->
            val cred = store.findActiveCredential(db.id)
            db.toResponse(
                host = instance.host,
                port = instance.port,
                secretRef = cred?.secretRef,
                username = cred?.username,
            )
        }
    }

    fun getDatabase(databaseId: UUID): DbDatabaseResponse {
        val db = store.findDatabaseById(databaseId)
            ?: throw ApiException.NotFound(
                "database not found",
                mapOf("id" to databaseId.toString()),
            )
        val instance = getInstance(db.instanceId)
        val cred = store.findActiveCredential(db.id)
        return db.toResponse(
            host = instance.host,
            port = instance.port,
            secretRef = cred?.secretRef,
            username = cred?.username,
        )
    }

    fun createDatabase(instanceId: UUID, nameRaw: String?): CreatedDatabase {
        val instance = getInstance(instanceId)
        if (instance.status != DbInstanceStatus.Available) {
            throw ApiException.Conflict(
                "database instance is not available",
                mapOf(
                    "instanceId" to instanceId.toString(),
                    "status" to instance.status.wire,
                ),
            )
        }
        val name = validateDatabaseName(nameRaw)
        rememberLocalEndpoint(
            instance.id,
            ProvisionResult(
                endpointRef = instance.endpointRef ?: "",
                host = instance.host,
                port = instance.port,
                containerId = instance.containerId,
            ),
        )

        val created = try {
            store.createDatabase(instanceId, name, status = DbDatabaseStatus.Provisioning)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "database name already exists on instance",
                mapOf("name" to name, "instanceId" to instanceId.toString()),
            )
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }

        val username = CredentialGenerator.username(name, created.id.toString().take(6))
        val password = CredentialGenerator.password(32)
        val started = System.nanoTime()
        return try {
            val result = provisioner.createDatabaseWithRole(
                instanceId = instanceId,
                databaseName = name,
                username = username,
                password = password,
            )
            isolation.assertNotControlDatabase(result.endpointRef)
            val secretName = "managed-db-${created.id}"
            val secretRef = secrets.putSecret(instance.projectId, secretName, password)
            val credential = store.createCredential(
                databaseId = created.id,
                username = username,
                secretRef = secretRef,
                status = "active",
            )
            val available = store.updateDatabaseStatus(created.id, DbDatabaseStatus.Available)
            telemetry.recordManagedDbProvisionDuration(
                (System.nanoTime() - started) / 1_000_000_000.0,
                "database",
            )
            log?.info(
                "managed db database available",
                "instance_id" to instanceId,
                "database_id" to created.id,
                "database" to name,
                "username" to username,
                "secret_ref" to secretRef,
            )
            CreatedDatabase(
                database = available,
                instance = instance,
                credential = credential,
                password = password,
            )
        } catch (e: Exception) {
            telemetry.recordManagedDbProvisionError("database")
            val reason = e.message ?: e.javaClass.simpleName
            store.updateDatabaseStatus(created.id, DbDatabaseStatus.Error, reason)
            log?.error(
                "managed db database provision failed",
                "instance_id" to instanceId,
                "database_id" to created.id,
                "error" to reason,
            )
            throw ApiException.BadRequest(
                "failed to provision database: $reason",
                mapOf("databaseId" to created.id.toString()),
            )
        }
    }

    fun toCreateResponse(created: CreatedDatabase): DbDatabaseResponse =
        created.database.toResponse(
            host = created.instance.host,
            port = created.instance.port,
            secretRef = created.credential.secretRef,
            username = created.credential.username,
            password = created.password,
        )

    /** Test/helper: refuse assigning Control's JDBC URL as a product endpoint. */
    fun assertEndpointAllowed(endpointRef: String?) {
        try {
            isolation.assertNotControlDatabase(endpointRef)
        } catch (e: IsolationViolation) {
            throw ApiException.BadRequest(
                e.message ?: "isolation invariant violated",
                mapOf("field" to "endpointRef"),
            )
        }
    }

    private fun rememberLocalEndpoint(instanceId: UUID, result: ProvisionResult) {
        val local = provisioner as? LocalProvisioner ?: return
        val host = result.host ?: return
        val port = result.port ?: return
        local.rememberEndpoint(
            instanceId,
            InstanceEndpoint(
                endpointRef = result.endpointRef,
                host = host,
                port = port,
                containerId = result.containerId,
            ),
        )
    }

    private fun transition(
        current: DbInstance,
        to: DbInstanceStatus,
        reason: String? = null,
        endpointRef: String? = null,
        host: String? = null,
        port: Int? = null,
        containerId: String? = null,
    ): DbInstance {
        DbInstanceStateMachine.requireTransition(current.status, to)
        if (endpointRef != null) {
            isolation.assertNotControlDatabase(endpointRef)
        }
        val updated = store.updateInstanceStatus(
            id = current.id,
            status = to,
            statusReason = reason,
            endpointRef = endpointRef,
            host = host,
            port = port,
            containerId = containerId,
        )
        telemetry.recordManagedDbInstance(to.wire)
        log?.info(
            "managed db instance status transition",
            "instance_id" to current.id,
            "from" to current.status.wire,
            "to" to to.wire,
            "reason" to reason,
        )
        return updated
    }

    private fun validateDatabaseName(nameRaw: String?): String {
        val name = nameRaw?.trim().orEmpty()
        if (name.isEmpty()) {
            throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
        }
        if (!PostgresAdmin.isSafeIdent(name)) {
            throw ApiException.BadRequest(
                "name must be a postgres identifier: lowercase letter/underscore, then [a-z0-9_], max 63",
                mapOf("field" to "name"),
            )
        }
        return name
    }

    private fun mapRepo(e: RepositoryException): ApiException =
        when (e) {
            is RepositoryException.Conflict -> ApiException.Conflict(e.message ?: "conflict")
            is RepositoryException.NotFound -> ApiException.NotFound(e.message ?: "not found")
            is RepositoryException.ConstraintViolation ->
                ApiException.BadRequest(e.message ?: "constraint violation")
        }
}
