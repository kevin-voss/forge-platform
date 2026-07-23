package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.ProjectRepository
import forge.control.service.RelationshipValidator
import java.nio.file.Files
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class RotationDeletionProtectionTest {
    @Test
    fun deletionGuardBlocksWhenProtectedAndAllowsWhenDisabledPlusForce() {
        val id = UUID.randomUUID()
        assertFailsWith<ApiException.Conflict> {
            DeletionGuard.assertCanDelete("db_instance", id, deletionProtection = true, force = true)
        }
        assertFailsWith<ApiException.Conflict> {
            DeletionGuard.assertCanDelete("db_instance", id, deletionProtection = false, force = false)
        }
        DeletionGuard.assertCanDelete("db_instance", id, deletionProtection = false, force = true)
    }

    @Test
    fun attachmentsBlockDeleteByDefault() {
        val id = UUID.randomUUID()
        assertFailsWith<ApiException.Conflict> {
            DeletionGuard.assertNoAttachments(id, attachmentCount = 1)
        }
        DeletionGuard.assertNoAttachments(id, attachmentCount = 0)
    }

    @Test
    fun rotationIssuesNewCredMarksOldRevokedAndInvalidatesOldPassword() {
        val ctx = harness()
        val created = ctx.service.createDatabase(ctx.instanceId, "appdb")
        val oldUser = created.credential.username
        val oldPass = created.password
        assertTrue(ctx.provisioner.canAuthenticate(ctx.instanceId, "appdb", oldUser, oldPass))

        val rotated = ctx.service.rotateCredentials(created.database.id)
        assertNotNull(rotated.secretRef)
        assertEquals(rotated.secretRef, rotated.credential.secretRef)
        assertTrue(rotated.credential.password.isNotBlank())
        assertTrue(rotated.credential.username != oldUser)

        val oldCred = ctx.store.findCredentialById(created.credential.id)!!
        assertEquals("revoked", oldCred.status)
        assertNotNull(oldCred.revokedAt)
        assertFalse(ctx.provisioner.canAuthenticate(ctx.instanceId, "appdb", oldUser, oldPass))
        assertTrue(
            ctx.provisioner.canAuthenticate(
                ctx.instanceId,
                "appdb",
                rotated.credential.username,
                rotated.credential.password,
            ),
        )
        assertEquals(rotated.credential.password, ctx.secrets.getSecret(rotated.secretRef))
    }

    @Test
    fun rotationFailureKeepsOldCredentialsWorking() {
        val ctx = harness()
        val created = ctx.service.createDatabase(ctx.instanceId, "appdb")
        val oldUser = created.credential.username
        val oldPass = created.password
        ctx.provisioner.failNextRoleCreate = true

        assertFailsWith<ApiException.BadRequest> {
            ctx.service.rotateCredentials(created.database.id)
        }
        val oldCred = ctx.store.findCredentialById(created.credential.id)!!
        assertEquals("active", oldCred.status)
        assertTrue(ctx.provisioner.canAuthenticate(ctx.instanceId, "appdb", oldUser, oldPass))
    }

    @Test
    fun deleteProtectedInstanceReturns409ThenSucceedsAfterDisableAndForce() {
        val ctx = harness()
        ctx.service.createDatabase(ctx.instanceId, "appdb")

        assertFailsWith<ApiException.Conflict> {
            ctx.service.deleteInstance(ctx.instanceId, force = true)
        }
        assertFailsWith<ApiException.Conflict> {
            ctx.service.deleteInstance(ctx.instanceId, force = false)
        }

        ctx.service.patchInstanceDeletionProtection(ctx.instanceId, false)
        ctx.service.deleteInstance(ctx.instanceId, force = true)
        assertEquals(null, ctx.store.findInstanceById(ctx.instanceId))
        assertTrue(ctx.archivesPutCount > 0, "pre-delete backup should store an archive")
    }

    @Test
    fun attachedDatabaseDeleteIsBlocked() {
        val ctx = harness()
        val created = ctx.service.createDatabase(ctx.instanceId, "appdb")
        ctx.service.attach(created.database.id, ctx.applicationId.toString(), "DATABASE_URL")
        ctx.service.patchDatabaseDeletionProtection(created.database.id, false)
        assertFailsWith<ApiException.Conflict> {
            ctx.service.deleteDatabase(created.database.id, force = true)
        }
        ctx.service.detach(ctx.store.listAttachmentsByDatabase(created.database.id).single().id)
        ctx.service.deleteDatabase(created.database.id, force = true)
        assertEquals(null, ctx.store.findDatabaseById(created.database.id))
    }

    @Test
    fun rotationUpdatesAttachmentUrlSecret() {
        val ctx = harness()
        val created = ctx.service.createDatabase(ctx.instanceId, "appdb")
        val attachment = ctx.service.attach(created.database.id, ctx.applicationId.toString(), "DATABASE_URL")
        val oldUrl = ctx.secrets.getSecret(attachment.secretRef!!)!!
        assertTrue(oldUrl.contains(created.credential.username))

        val rotated = ctx.service.rotateCredentials(created.database.id)
        val newUrl = ctx.secrets.getSecret(attachment.secretRef!!)!!
        assertTrue(newUrl.contains(rotated.credential.username))
        assertFalse(newUrl.contains(created.credential.username))
        assertTrue(newUrl.contains(rotated.credential.password))
    }

    private fun harness(): Harness {
        val projectId = UUID.randomUUID()
        val applicationId = UUID.randomUUID()
        val isolation = IsolationGuard("jdbc:postgresql://127.0.0.1:5001/forge", "forge")
        val provisioner = ControllableFakeProvisioner(isolation)
        val store = FullInMemoryManagedDbRepository()
        val secrets = InMemoryManagedDbSecretsClient()
        val backupDir = Files.createTempDirectory("mdb-rot")
        var archivesPutCount = 0
        val archives = object : ArchiveStore {
            override fun put(projectId: UUID, backupId: UUID, bytes: ByteArray): String {
                archivesPutCount++
                val path = backupDir.resolve("$backupId.dump")
                Files.write(path, bytes)
                return "volume://test/$backupId.dump"
            }

            override fun get(projectId: UUID, location: String): ByteArray? = null
            override fun delete(projectId: UUID, location: String) = Unit
        }
        val now = Instant.now()
        val instanceId = UUID.randomUUID()
        store.putInstance(
            DbInstance(
                id = instanceId,
                projectId = projectId,
                name = "main",
                status = DbInstanceStatus.Available,
                host = "127.0.0.1",
                port = 5433,
                containerId = "cid",
                endpointRef = "fake://managed-db/$instanceId",
                createdAt = now,
                updatedAt = now,
            ),
        )
        val projects = object : ProjectRepository {
            override fun create(name: String, slug: String) = error("unused")
            override fun findById(id: UUID) =
                if (id == projectId) {
                    forge.control.domain.Project(id, "p", "p", now, now)
                } else {
                    null
                }
            override fun list() = emptyList<forge.control.domain.Project>()
            override fun update(id: UUID, name: String?, slug: String?) = error("unused")
            override fun delete(id: UUID) = Unit
        }
        val apps = object : ApplicationRepository {
            override fun create(projectId: UUID, name: String) = error("unused")
            override fun findById(id: UUID) =
                if (id == applicationId) {
                    forge.control.domain.Application(id, projectId, "app", now, now)
                } else {
                    null
                }
            override fun list(projectId: UUID) = emptyList<forge.control.domain.Application>()
            override fun update(id: UUID, name: String) = error("unused")
            override fun delete(id: UUID) = Unit
        }
        val rotation = RotationRunner(
            store = store,
            provisioner = provisioner,
            isolation = isolation,
            secrets = secrets,
            graceSeconds = 0,
        )
        val service = ManagedDbService(
            store = store,
            provisioner = provisioner,
            isolation = isolation,
            relationships = RelationshipValidator(projects, apps),
            secrets = secrets,
            applications = apps,
            archives = archives,
            rotationRunner = rotation,
            predeleteBackup = true,
        )
        return Harness(
            service = service,
            store = store,
            provisioner = provisioner,
            secrets = secrets,
            instanceId = instanceId,
            applicationId = applicationId,
            archivesPutCountProvider = { archivesPutCount },
        )
    }

    private class Harness(
        val service: ManagedDbService,
        val store: FullInMemoryManagedDbRepository,
        val provisioner: ControllableFakeProvisioner,
        val secrets: InMemoryManagedDbSecretsClient,
        val instanceId: UUID,
        val applicationId: UUID,
        private val archivesPutCountProvider: () -> Int,
    ) {
        val archivesPutCount: Int get() = archivesPutCountProvider()
    }
}

/** FakeProvisioner that can inject createRoleOnDatabase failures. */
class ControllableFakeProvisioner(
    isolation: IsolationGuard,
) : Provisioner {
    private val inner = FakeProvisioner(isolation)
    var failNextRoleCreate: Boolean = false

    override fun createInstance(instanceId: UUID, projectId: UUID, name: String) =
        inner.createInstance(instanceId, projectId, name)

    override fun deleteInstance(instanceId: UUID) = inner.deleteInstance(instanceId)

    override fun createDatabase(instanceId: UUID, databaseName: String) =
        inner.createDatabase(instanceId, databaseName)

    override fun createRole(databaseId: UUID, username: String) =
        inner.createRole(databaseId, username)

    override fun createDatabaseWithRole(
        instanceId: UUID,
        databaseName: String,
        username: String,
        password: String,
    ) = inner.createDatabaseWithRole(instanceId, databaseName, username, password)

    override fun dumpDatabase(instanceId: UUID, databaseName: String) =
        inner.dumpDatabase(instanceId, databaseName)

    override fun restoreDatabase(instanceId: UUID, databaseName: String, archive: ByteArray) =
        inner.restoreDatabase(instanceId, databaseName, archive)

    override fun createRoleOnDatabase(
        instanceId: UUID,
        databaseName: String,
        username: String,
        password: String,
    ): ProvisionResult {
        if (failNextRoleCreate) {
            failNextRoleCreate = false
            throw ProvisionerException("injected rotation failure")
        }
        return inner.createRoleOnDatabase(instanceId, databaseName, username, password)
    }

    override fun revokeRole(instanceId: UUID, username: String, reassignTo: String?) =
        inner.revokeRole(instanceId, username, reassignTo)

    override fun dropDatabase(instanceId: UUID, databaseName: String, roleNames: List<String>) =
        inner.dropDatabase(instanceId, databaseName, roleNames)

    fun canAuthenticate(instanceId: UUID, databaseName: String, username: String, password: String) =
        inner.canAuthenticate(instanceId, databaseName, username, password)
}

/** Full in-memory store supporting rotation + delete flows. */
class FullInMemoryManagedDbRepository : ManagedDbRepository {
    private val instances = ConcurrentHashMap<UUID, DbInstance>()
    private val databases = ConcurrentHashMap<UUID, DbDatabase>()
    private val credentials = ConcurrentHashMap<UUID, DbCredential>()
    private val attachments = ConcurrentHashMap<UUID, DbAttachment>()
    private val backups = ConcurrentHashMap<UUID, DbBackup>()

    fun putInstance(instance: DbInstance) {
        instances[instance.id] = instance
    }

    override fun createInstance(
        projectId: UUID,
        name: String,
        status: DbInstanceStatus,
        engine: String,
        deletionProtection: Boolean,
    ): DbInstance {
        val row = DbInstance(
            id = UUID.randomUUID(),
            projectId = projectId,
            name = name,
            status = status,
            engine = engine,
            deletionProtection = deletionProtection,
            createdAt = Instant.now(),
            updatedAt = Instant.now(),
        )
        instances[row.id] = row
        return row
    }

    override fun findInstanceById(id: UUID) = instances[id]
    override fun listInstances(projectId: UUID) = instances.values.filter { it.projectId == projectId }
    override fun updateInstanceStatus(
        id: UUID,
        status: DbInstanceStatus,
        statusReason: String?,
        endpointRef: String?,
        host: String?,
        port: Int?,
        containerId: String?,
    ): DbInstance {
        val existing = instances[id]!!
        val updated = existing.copy(
            status = status,
            statusReason = statusReason,
            endpointRef = endpointRef ?: existing.endpointRef,
            host = host ?: existing.host,
            port = port ?: existing.port,
            containerId = containerId ?: existing.containerId,
            updatedAt = Instant.now(),
        )
        instances[id] = updated
        return updated
    }

    override fun updateInstanceDeletionProtection(id: UUID, deletionProtection: Boolean): DbInstance {
        val updated = instances[id]!!.copy(deletionProtection = deletionProtection, updatedAt = Instant.now())
        instances[id] = updated
        return updated
    }

    override fun listDatabases(instanceId: UUID) = databases.values.filter { it.instanceId == instanceId }
    override fun findDatabaseById(id: UUID) = databases[id]

    override fun createDatabase(
        instanceId: UUID,
        name: String,
        status: DbDatabaseStatus,
        deletionProtection: Boolean,
    ): DbDatabase {
        val row = DbDatabase(
            id = UUID.randomUUID(),
            instanceId = instanceId,
            name = name,
            status = status,
            deletionProtection = deletionProtection,
            createdAt = Instant.now(),
        )
        databases[row.id] = row
        return row
    }

    override fun updateDatabaseStatus(id: UUID, status: DbDatabaseStatus, statusReason: String?): DbDatabase {
        val updated = databases[id]!!.copy(status = status, statusReason = statusReason)
        databases[id] = updated
        return updated
    }

    override fun updateDatabaseDeletionProtection(id: UUID, deletionProtection: Boolean): DbDatabase {
        val updated = databases[id]!!.copy(deletionProtection = deletionProtection)
        databases[id] = updated
        return updated
    }

    override fun createCredential(
        databaseId: UUID,
        username: String,
        secretRef: String?,
        status: String,
    ): DbCredential {
        val row = DbCredential(
            id = UUID.randomUUID(),
            databaseId = databaseId,
            username = username,
            secretRef = secretRef,
            status = status,
            createdAt = Instant.now(),
        )
        credentials[row.id] = row
        return row
    }

    override fun findActiveCredential(databaseId: UUID) =
        credentials.values.filter { it.databaseId == databaseId && it.status == "active" }
            .maxByOrNull { it.createdAt }

    override fun findCredentialById(id: UUID) = credentials[id]
    override fun listCredentials(databaseId: UUID) =
        credentials.values.filter { it.databaseId == databaseId }

    override fun updateCredentialStatus(
        id: UUID,
        status: String,
        rotatedAt: Instant?,
        revokedAt: Instant?,
    ): DbCredential {
        val existing = credentials[id]!!
        val updated = existing.copy(
            status = status,
            rotatedAt = rotatedAt ?: existing.rotatedAt,
            revokedAt = revokedAt ?: existing.revokedAt,
        )
        credentials[id] = updated
        return updated
    }

    override fun markCredentialRotated(id: UUID) =
        updateCredentialStatus(id, status = "active", rotatedAt = Instant.now())

    override fun createAttachment(
        databaseId: UUID,
        applicationId: UUID,
        envVar: String,
        secretRef: String?,
        id: UUID,
    ): DbAttachment {
        val row = DbAttachment(id, databaseId, applicationId, envVar, secretRef, Instant.now())
        attachments[id] = row
        return row
    }

    override fun findAttachmentById(id: UUID) = attachments[id]
    override fun listAttachmentsByApplication(applicationId: UUID) =
        attachments.values.filter { it.applicationId == applicationId }
    override fun listAttachmentsByDatabase(databaseId: UUID) =
        attachments.values.filter { it.databaseId == databaseId }

    override fun deleteAttachment(id: UUID) {
        attachments.remove(id)
    }

    override fun deleteDatabase(id: UUID) {
        databases.remove(id)
    }

    override fun deleteCredential(id: UUID) {
        credentials.remove(id)
    }

    override fun deleteBackupsForDatabase(databaseId: UUID) {
        backups.entries.removeIf { it.value.databaseId == databaseId }
    }

    override fun deleteInstance(id: UUID) {
        instances.remove(id)
    }

    override fun createBackup(databaseId: UUID, status: DbBackupStatus, id: UUID): DbBackup {
        val row = DbBackup(id, databaseId, null, status, createdAt = Instant.now())
        backups[id] = row
        return row
    }

    override fun findBackupById(id: UUID) = backups[id]
    override fun listBackups(databaseId: UUID) =
        backups.values.filter { it.databaseId == databaseId }

    override fun updateBackup(
        id: UUID,
        status: DbBackupStatus,
        location: String?,
        checksum: String?,
        sizeBytes: Long?,
        statusReason: String?,
        completedAt: Instant?,
    ): DbBackup {
        val existing = backups[id]!!
        val updated = existing.copy(
            status = status,
            location = location ?: existing.location,
            checksum = checksum ?: existing.checksum,
            sizeBytes = sizeBytes ?: existing.sizeBytes,
            statusReason = statusReason,
            completedAt = completedAt ?: existing.completedAt,
        )
        backups[id] = updated
        return updated
    }

    override fun updateBackupRestore(
        id: UUID,
        restoreStatus: DbRestoreStatus,
        restoreTargetDatabaseId: UUID?,
        restoreStatusReason: String?,
        restoreCompletedAt: Instant?,
    ): DbBackup = error("unused")
}
