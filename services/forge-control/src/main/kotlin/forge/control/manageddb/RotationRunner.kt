package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

data class RotationResult(
    val credential: DbCredential,
    /** One-time plaintext password for the rotate response only. */
    val password: String,
    val secretRef: String,
    val oldCredentialId: UUID,
)

/**
 * Issues a new role/password, updates Secrets (credential + attachment URLs),
 * then revokes the old role after a grace window so there is no outage gap.
 *
 * On mid-flight failure the old credentials remain valid.
 */
class RotationRunner(
    private val store: ManagedDbRepository,
    private val provisioner: Provisioner,
    private val isolation: IsolationGuard,
    private val secrets: ManagedDbSecretsClient,
    private val graceSeconds: Long = 60,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val sleepMs: (Long) -> Unit = { ms -> Thread.sleep(ms) },
) {
    private val revokeExecutor = Executors.newSingleThreadExecutor { r ->
        Thread(r, "managed-db-rotation-revoke").apply { isDaemon = true }
    }

    fun rotate(databaseId: UUID): RotationResult {
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
        val instance = store.findInstanceById(database.instanceId)
            ?: throw ApiException.NotFound(
                "database instance not found",
                mapOf("id" to database.instanceId.toString()),
            )
        rememberLocalEndpoint(instance)
        val old = store.findActiveCredential(databaseId)
            ?: throw ApiException.Conflict(
                "database has no active credential",
                mapOf("databaseId" to databaseId.toString()),
            )

        store.updateCredentialStatus(old.id, status = "rotating")

        val newUsername = CredentialGenerator.username(
            database.name,
            "r" + UUID.randomUUID().toString().replace("-", "").take(6),
        )
        val newPassword = CredentialGenerator.password(32)
        var provisionedUsername: String? = null

        return try {
            val result = provisioner.createRoleOnDatabase(
                instanceId = instance.id,
                databaseName = database.name,
                username = newUsername,
                password = newPassword,
            )
            provisionedUsername = result.username ?: newUsername
            isolation.assertNotControlDatabase(result.endpointRef)

            val secretName = "managed-db-${UUID.randomUUID()}"
            val secretRef = secrets.putSecret(instance.projectId, secretName, newPassword)
            val created = store.createCredential(
                databaseId = databaseId,
                username = provisionedUsername!!,
                secretRef = secretRef,
                status = "active",
            )
            val rotatedCred = store.markCredentialRotated(created.id)

            refreshAttachmentUrls(
                database = database,
                instance = instance,
                username = provisionedUsername,
                password = newPassword,
            )

            scheduleRevoke(
                instanceId = instance.id,
                oldCredentialId = old.id,
                oldUsername = old.username,
                reassignTo = provisionedUsername,
                oldSecretRef = old.secretRef,
            )

            telemetry.recordManagedDbRotation("succeeded")
            log?.info(
                "managed db credentials rotated",
                "database_id" to databaseId,
                "old_credential_id" to old.id,
                "new_credential_id" to rotatedCred.id,
                "username" to provisionedUsername,
                "secret_ref" to secretRef,
                "grace_seconds" to graceSeconds,
            )
            RotationResult(
                credential = rotatedCred,
                password = newPassword,
                secretRef = secretRef,
                oldCredentialId = old.id,
            )
        } catch (e: Exception) {
            telemetry.recordManagedDbRotation("failed")
            // Keep old credentials valid — no outage window.
            try {
                store.updateCredentialStatus(old.id, status = "active")
            } catch (_: Exception) {
                // best effort
            }
            provisionedUsername?.let { user ->
                try {
                    provisioner.revokeRole(instance.id, user, reassignTo = old.username)
                } catch (_: Exception) {
                    // best effort rollback of partial role
                }
            }
            val reason = e.message ?: e.javaClass.simpleName
            log?.error(
                "managed db credential rotation failed",
                "database_id" to databaseId,
                "old_credential_id" to old.id,
                "error" to reason,
            )
            if (e is ApiException) throw e
            throw ApiException.BadRequest(
                "failed to rotate credentials: $reason",
                mapOf("databaseId" to databaseId.toString()),
            )
        }
    }

    private fun refreshAttachmentUrls(
        database: DbDatabase,
        instance: DbInstance,
        username: String,
        password: String,
    ) {
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
        val url = ConnectionUrl.compose(
            username = username,
            password = password,
            host = host,
            port = port,
            database = database.name,
        )
        isolation.assertNotControlDatabase(url)
        for (attachment in store.listAttachmentsByDatabase(database.id)) {
            val ref = attachment.secretRef ?: continue
            val parsed = HttpManagedDbSecretsClient.parseSecretRef(ref) ?: continue
            secrets.putSecret(
                projectId = UUID.fromString(parsed.projectId),
                secretName = parsed.name,
                value = url,
            )
        }
    }

    private fun scheduleRevoke(
        instanceId: UUID,
        oldCredentialId: UUID,
        oldUsername: String,
        reassignTo: String,
        oldSecretRef: String?,
    ) {
        val delayMs = (graceSeconds.coerceAtLeast(0)) * 1000
        val task = Runnable {
            try {
                if (delayMs > 0) sleepMs(delayMs)
                provisioner.revokeRole(instanceId, oldUsername, reassignTo = reassignTo)
                store.updateCredentialStatus(
                    oldCredentialId,
                    status = "revoked",
                    revokedAt = java.time.Instant.now(),
                )
                oldSecretRef?.let { ref ->
                    try {
                        secrets.deleteSecret(ref)
                    } catch (_: Exception) {
                        // best effort
                    }
                }
                log?.info(
                    "managed db old credential revoked",
                    "credential_id" to oldCredentialId,
                    "username" to oldUsername,
                    "instance_id" to instanceId,
                )
            } catch (e: Exception) {
                log?.error(
                    "managed db old credential revoke failed",
                    "credential_id" to oldCredentialId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
        if (delayMs == 0L) {
            task.run()
        } else {
            revokeExecutor.execute(task)
        }
    }

    private fun rememberLocalEndpoint(instance: DbInstance) {
        val local = provisioner as? LocalProvisioner ?: return
        val host = instance.host ?: return
        val port = instance.port ?: return
        local.rememberEndpoint(
            instance.id,
            InstanceEndpoint(
                endpointRef = instance.endpointRef ?: "",
                host = host,
                port = port,
                containerId = instance.containerId,
            ),
        )
    }

    fun shutdown() {
        revokeExecutor.shutdown()
        revokeExecutor.awaitTermination(5, TimeUnit.SECONDS)
    }
}
