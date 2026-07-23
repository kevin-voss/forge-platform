package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.ApplicationRepository
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
    private val applications: ApplicationRepository? = null,
    private val defaultEnvVar: String = "DATABASE_URL",
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) : AttachmentEnvSource {
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

    /**
     * Attach a managed database to an application: compose connection URL,
     * store it in Secrets, record attachment (secret_ref only in Control).
     * Never returns or logs the plaintext URL.
     */
    fun attach(
        databaseId: UUID,
        applicationIdRaw: String?,
        envVarRaw: String?,
    ): DbAttachment {
        val database = store.findDatabaseById(databaseId)
            ?: throw ApiException.NotFound(
                "database not found",
                mapOf("id" to databaseId.toString()),
            )
        if (database.status != DbDatabaseStatus.Available) {
            throw ApiException.Conflict(
                "database is not available",
                mapOf("databaseId" to databaseId.toString(), "status" to database.status.wire),
            )
        }
        val instance = getInstance(database.instanceId)
        val applicationId = parseUuid(applicationIdRaw, "applicationId")
        val application = applications?.findById(applicationId)
            ?: throw ApiException.NotFound(
                "application not found",
                mapOf("id" to applicationId.toString()),
            )
        if (application.projectId != instance.projectId) {
            // Cross-project attach is denied without leaking the other project's existence.
            throw ApiException.NotFound(
                "application not found",
                mapOf("id" to applicationId.toString()),
            )
        }
        val envVar = resolveEnvVar(envVarRaw)
        val credential = store.findActiveCredential(databaseId)
            ?: throw ApiException.Conflict(
                "database has no active credential",
                mapOf("databaseId" to databaseId.toString()),
            )
        val secretRefCred = credential.secretRef
            ?: throw ApiException.Conflict(
                "database credential has no secret_ref",
                mapOf("databaseId" to databaseId.toString()),
            )
        val host = instance.host
            ?: throw ApiException.Conflict(
                "database instance has no host",
                mapOf("instanceId" to instance.id.toString()),
            )
        val port = instance.port
            ?: throw ApiException.Conflict(
                "database instance has no port",
                mapOf("instanceId" to instance.id.toString()),
            )
        val password = try {
            secrets.getSecret(secretRefCred)
        } catch (e: ManagedDbSecretsException) {
            throw ApiException.BadRequest(
                "failed to read credential secret: ${e.message}",
                mapOf("databaseId" to databaseId.toString()),
            )
        } ?: throw ApiException.Conflict(
            "credential secret missing in Secrets",
            mapOf("databaseId" to databaseId.toString()),
        )
        val url = ConnectionUrl.compose(
            username = credential.username,
            password = password,
            host = host,
            port = port,
            database = database.name,
        )
        isolation.assertNotControlDatabase(url)

        val attachmentId = UUID.randomUUID()
        val urlSecretName = "managed-db-url-$attachmentId"
        val urlSecretRef = try {
            secrets.putSecret(instance.projectId, urlSecretName, url)
        } catch (e: ManagedDbSecretsException) {
            throw ApiException.BadRequest(
                "failed to store connection URL secret: ${e.message}",
                mapOf("databaseId" to databaseId.toString()),
            )
        }

        val created = try {
            store.createAttachment(
                id = attachmentId,
                databaseId = databaseId,
                applicationId = applicationId,
                envVar = envVar,
                secretRef = urlSecretRef,
            )
        } catch (e: RepositoryException.Conflict) {
            try {
                secrets.deleteSecret(urlSecretRef)
            } catch (_: Exception) {
                // best effort
            }
            throw ApiException.Conflict(
                "database already attached to application",
                mapOf(
                    "databaseId" to databaseId.toString(),
                    "applicationId" to applicationId.toString(),
                ),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            try {
                secrets.deleteSecret(urlSecretRef)
            } catch (_: Exception) {
                // best effort
            }
            relationships.requireApplication(applicationId)
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            try {
                secrets.deleteSecret(urlSecretRef)
            } catch (_: Exception) {
                // best effort
            }
            throw mapRepo(e)
        }

        telemetry.recordManagedDbAttachment()
        log?.info(
            "managed db attached",
            "attachment_id" to created.id,
            "database_id" to databaseId,
            "application_id" to applicationId,
            "env_var" to envVar,
            "secret_ref" to created.secretRef,
        )
        return created
    }

    fun detach(attachmentId: UUID) {
        val existing = store.findAttachmentById(attachmentId)
            ?: throw ApiException.NotFound(
                "attachment not found",
                mapOf("id" to attachmentId.toString()),
            )
        store.deleteAttachment(attachmentId)
        existing.secretRef?.let { ref ->
            try {
                secrets.deleteSecret(ref)
            } catch (_: Exception) {
                // Detach must succeed even if Secrets cleanup fails; next deploy won't inject.
            }
        }
        log?.info(
            "managed db detached",
            "attachment_id" to attachmentId,
            "database_id" to existing.databaseId,
            "application_id" to existing.applicationId,
            "env_var" to existing.envVar,
        )
    }

    fun listAttachmentsForApplication(applicationId: UUID): List<DbAttachmentResponse> {
        relationships.requireApplication(applicationId)
        return store.listAttachmentsByApplication(applicationId).map { it.toResponse() }
    }

    fun getAttachment(attachmentId: UUID): DbAttachment =
        store.findAttachmentById(attachmentId)
            ?: throw ApiException.NotFound(
                "attachment not found",
                mapOf("id" to attachmentId.toString()),
            )

    /**
     * Resolve attached connection URL env vars for Runtime injection.
     * Holds deploy when a required attachment secret cannot be read.
     */
    override fun resolveForApplication(applicationId: String): AttachmentEnvResult {
        if (applicationId.isBlank()) return AttachmentEnvResult.Empty
        val appId = try {
            UUID.fromString(applicationId)
        } catch (_: IllegalArgumentException) {
            return AttachmentEnvResult.Empty
        }
        val attachments = store.listAttachmentsByApplication(appId)
        if (attachments.isEmpty()) return AttachmentEnvResult.Empty

        val env = linkedMapOf<String, String>()
        val refs = mutableListOf<String>()
        for (attachment in attachments) {
            val ref = attachment.secretRef
            if (ref.isNullOrBlank()) {
                return AttachmentEnvResult.Hold(
                    "attachment_missing_secret_ref:${attachment.id}",
                )
            }
            val value = try {
                secrets.getSecret(ref)
            } catch (e: ManagedDbSecretsException) {
                return AttachmentEnvResult.Hold(
                    "attachment_secret_unavailable:${attachment.id}:${e.message}",
                )
            }
            if (value.isNullOrBlank()) {
                return AttachmentEnvResult.Hold(
                    "attachment_secret_missing:${attachment.id}",
                )
            }
            env[attachment.envVar] = value
            refs += ref
        }
        val fingerprint = refs.sorted().joinToString("|")
        return AttachmentEnvResult.Ready(env = env, fingerprint = fingerprint)
    }

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

    private fun resolveEnvVar(envVarRaw: String?): String {
        val raw = envVarRaw?.trim().orEmpty().ifEmpty { defaultEnvVar.trim() }
        if (raw.isEmpty()) {
            throw ApiException.BadRequest("env_var is required", mapOf("field" to "envVar"))
        }
        if (!ENV_VAR_PATTERN.matches(raw)) {
            throw ApiException.BadRequest(
                "env_var must match [A-Za-z_][A-Za-z0-9_]*",
                mapOf("field" to "envVar"),
            )
        }
        return raw
    }

    private fun parseUuid(raw: String?, field: String): UUID {
        val value = raw?.trim().orEmpty()
        if (value.isEmpty()) {
            throw ApiException.BadRequest("$field is required", mapOf("field" to field))
        }
        return try {
            UUID.fromString(value)
        } catch (_: IllegalArgumentException) {
            throw ApiException.BadRequest("invalid UUID for $field", mapOf("field" to field))
        }
    }

    private fun mapRepo(e: RepositoryException): ApiException =
        when (e) {
            is RepositoryException.Conflict -> ApiException.Conflict(e.message ?: "conflict")
            is RepositoryException.NotFound -> ApiException.NotFound(e.message ?: "not found")
            is RepositoryException.ConstraintViolation ->
                ApiException.BadRequest(e.message ?: "constraint violation")
        }

    companion object {
        private val ENV_VAR_PATTERN = Regex("^[A-Za-z_][A-Za-z0-9_]*$")
    }
}
