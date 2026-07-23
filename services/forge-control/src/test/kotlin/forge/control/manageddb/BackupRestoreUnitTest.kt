package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.ProjectRepository
import forge.control.service.RelationshipValidator
import java.nio.file.Files
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executor
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class BackupRestoreUnitTest {
    private val syncExecutor = Executor { it.run() }

    @Test
    fun backupSucceedsWithChecksumAndRestoreRecoversFixture() {
        val projectId = UUID.randomUUID()
        val instanceId = UUID.randomUUID()
        val databaseId = UUID.randomUUID()
        val fixture = "fixture-v1-known-data".toByteArray()
        val ctx = harness(projectId, instanceId, databaseId, fixture)

        val backup = ctx.service.createBackup(databaseId, projectId)
        assertEquals(DbBackupStatus.Succeeded, backup.status.let {
            // create returns running; sync runner completes before return of enqueue
            ctx.store.findBackupById(backup.id)!!.status
        })
        val completed = ctx.store.findBackupById(backup.id)!!
        assertEquals(DbBackupStatus.Succeeded, completed.status)
        assertNotNull(completed.checksum)
        assertNotNull(completed.location)
        assertTrue(completed.sizeBytes!! > 0)

        // Mutate then restore.
        ctx.provisioner.seedDump(instanceId, "appdb", "mutated".toByteArray())
        val restore = ctx.service.restoreBackup(completed.id, databaseId.toString(), projectId)
        assertEquals("running", restore.status)
        val after = ctx.store.findBackupById(completed.id)!!
        assertEquals(DbRestoreStatus.Succeeded, after.restoreStatus)
        assertEquals(String(fixture), String(ctx.provisioner.currentDump(instanceId, "appdb")!!))
    }

    @Test
    fun corruptArchiveAbortsWithIntegrityError() {
        val projectId = UUID.randomUUID()
        val instanceId = UUID.randomUUID()
        val databaseId = UUID.randomUUID()
        val ctx = harness(projectId, instanceId, databaseId, "good".toByteArray())
        val backup = ctx.service.createBackup(databaseId, projectId)
        val completed = ctx.store.findBackupById(backup.id)!!
        // Corrupt on disk while keeping recorded checksum.
        val path = Files.list(ctx.backupDir.resolve(projectId.toString())).use { it.findFirst().get() }
        Files.write(path, "corrupt".toByteArray())

        val ex = assertFailsWith<ApiException.BadRequest> {
            ctx.service.restoreBackup(completed.id, databaseId.toString(), projectId)
        }
        assertEquals("integrity_error", ex.code)
    }

    @Test
    fun crossProjectBackupAccessIs404() {
        val projectA = UUID.randomUUID()
        val projectB = UUID.randomUUID()
        val instanceId = UUID.randomUUID()
        val databaseId = UUID.randomUUID()
        val ctx = harness(projectA, instanceId, databaseId, "x".toByteArray(), extraProject = projectB)
        assertFailsWith<ApiException.NotFound> {
            ctx.service.createBackup(databaseId, projectB)
        }
        assertFailsWith<ApiException.NotFound> {
            ctx.service.listBackups(databaseId, projectB)
        }
    }

    @Test
    fun dumpFailureMarksBackupFailedAndRemovesPartial() {
        val projectId = UUID.randomUUID()
        val instanceId = UUID.randomUUID()
        val databaseId = UUID.randomUUID()
        val store = InMemoryManagedDbRepository()
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
                createdAt = Instant.now(),
                updatedAt = Instant.now(),
            ),
        )
        store.putDatabase(
            DbDatabase(
                id = databaseId,
                instanceId = instanceId,
                name = "appdb",
                status = DbDatabaseStatus.Available,
                createdAt = Instant.now(),
            ),
        )
        val isolation = IsolationGuard("jdbc:postgresql://127.0.0.1:5001/forge", "forge")
        val provisioner = object : Provisioner by FakeProvisioner(isolation) {
            override fun dumpDatabase(instanceId: UUID, databaseName: String): DumpArchive {
                throw ProvisionerException("pg_dump exploded")
            }
        }
        val backupDir = Files.createTempDirectory("forge-mdb-fail")
        val archives = VolumeArchiveStore(backupDir)
        val projects = MapProjectRepository(setOf(projectId))
        val relationships = RelationshipValidator(projects, EmptyApplicationRepository)
        val backupRunner = BackupRunner(store, provisioner, archives, executor = syncExecutor)
        val service = ManagedDbService(
            store = store,
            provisioner = provisioner,
            isolation = isolation,
            relationships = relationships,
            secrets = InMemoryManagedDbSecretsClient(),
            backupRunner = backupRunner,
            restoreRunner = RestoreRunner(store, provisioner, archives, executor = syncExecutor),
            archives = archives,
        )
        val backup = service.createBackup(databaseId, projectId)
        val failed = store.findBackupById(backup.id)!!
        assertEquals(DbBackupStatus.Failed, failed.status)
        assertTrue(failed.statusReason!!.contains("pg_dump exploded"))
        val projectDir = backupDir.resolve(projectId.toString())
        assertTrue(!Files.exists(projectDir) || Files.list(projectDir).use { it.count() } == 0L)
    }

    private fun harness(
        projectId: UUID,
        instanceId: UUID,
        databaseId: UUID,
        fixture: ByteArray,
        extraProject: UUID? = null,
    ): Harness {
        val store = InMemoryManagedDbRepository()
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
                createdAt = Instant.now(),
                updatedAt = Instant.now(),
            ),
        )
        store.putDatabase(
            DbDatabase(
                id = databaseId,
                instanceId = instanceId,
                name = "appdb",
                status = DbDatabaseStatus.Available,
                createdAt = Instant.now(),
            ),
        )
        val isolation = IsolationGuard("jdbc:postgresql://127.0.0.1:5001/forge", "forge")
        val provisioner = FakeProvisioner(isolation)
        provisioner.seedDump(instanceId, "appdb", fixture)
        val backupDir = Files.createTempDirectory("forge-mdb-bak")
        val archives = VolumeArchiveStore(backupDir)
        val projects = MapProjectRepository(setOfNotNull(projectId, extraProject))
        val relationships = RelationshipValidator(projects, EmptyApplicationRepository)
        val backupRunner = BackupRunner(store, provisioner, archives, executor = syncExecutor)
        val restoreRunner = RestoreRunner(store, provisioner, archives, executor = syncExecutor)
        val service = ManagedDbService(
            store = store,
            provisioner = provisioner,
            isolation = isolation,
            relationships = relationships,
            secrets = InMemoryManagedDbSecretsClient(),
            backupRunner = backupRunner,
            restoreRunner = restoreRunner,
            archives = archives,
        )
        return Harness(service, store, provisioner, backupDir)
    }

    private data class Harness(
        val service: ManagedDbService,
        val store: InMemoryManagedDbRepository,
        val provisioner: FakeProvisioner,
        val backupDir: java.nio.file.Path,
    )
}

/** Minimal in-memory store for backup unit tests. */
class InMemoryManagedDbRepository : ManagedDbRepository {
    private val instances = ConcurrentHashMap<UUID, DbInstance>()
    private val databases = ConcurrentHashMap<UUID, DbDatabase>()
    private val backups = ConcurrentHashMap<UUID, DbBackup>()

    fun putInstance(instance: DbInstance) {
        instances[instance.id] = instance
    }

    fun putDatabase(database: DbDatabase) {
        databases[database.id] = database
    }

    override fun createInstance(
        projectId: UUID,
        name: String,
        status: DbInstanceStatus,
        engine: String,
        deletionProtection: Boolean,
    ): DbInstance = error("unused")

    override fun findInstanceById(id: UUID): DbInstance? = instances[id]
    override fun listInstances(projectId: UUID): List<DbInstance> =
        instances.values.filter { it.projectId == projectId }

    override fun updateInstanceStatus(
        id: UUID,
        status: DbInstanceStatus,
        statusReason: String?,
        endpointRef: String?,
        host: String?,
        port: Int?,
        containerId: String?,
    ): DbInstance = error("unused")

    override fun updateInstanceDeletionProtection(id: UUID, deletionProtection: Boolean): DbInstance =
        error("unused")

    override fun listDatabases(instanceId: UUID): List<DbDatabase> =
        databases.values.filter { it.instanceId == instanceId }

    override fun findDatabaseById(id: UUID): DbDatabase? = databases[id]
    override fun createDatabase(
        instanceId: UUID,
        name: String,
        status: DbDatabaseStatus,
        deletionProtection: Boolean,
    ): DbDatabase = error("unused")

    override fun updateDatabaseStatus(id: UUID, status: DbDatabaseStatus, statusReason: String?): DbDatabase =
        error("unused")

    override fun updateDatabaseDeletionProtection(id: UUID, deletionProtection: Boolean): DbDatabase =
        error("unused")

    override fun createCredential(
        databaseId: UUID,
        username: String,
        secretRef: String?,
        status: String,
    ): DbCredential = error("unused")

    override fun findActiveCredential(databaseId: UUID): DbCredential? = null
    override fun findCredentialById(id: UUID): DbCredential? = null
    override fun listCredentials(databaseId: UUID): List<DbCredential> = emptyList()
    override fun updateCredentialStatus(
        id: UUID,
        status: String,
        rotatedAt: Instant?,
        revokedAt: Instant?,
    ): DbCredential = error("unused")

    override fun markCredentialRotated(id: UUID): DbCredential = error("unused")

    override fun createAttachment(
        databaseId: UUID,
        applicationId: UUID,
        envVar: String,
        secretRef: String?,
        id: UUID,
    ): DbAttachment = error("unused")

    override fun findAttachmentById(id: UUID): DbAttachment? = null
    override fun listAttachmentsByApplication(applicationId: UUID): List<DbAttachment> = emptyList()
    override fun listAttachmentsByDatabase(databaseId: UUID): List<DbAttachment> = emptyList()
    override fun deleteAttachment(id: UUID) = Unit
    override fun deleteDatabase(id: UUID) = Unit
    override fun deleteCredential(id: UUID) = Unit
    override fun deleteBackupsForDatabase(databaseId: UUID) = Unit
    override fun deleteInstance(id: UUID) = Unit

    override fun createBackup(databaseId: UUID, status: DbBackupStatus, id: UUID): DbBackup {
        val backup = DbBackup(
            id = id,
            databaseId = databaseId,
            location = null,
            status = status,
            createdAt = Instant.now(),
        )
        backups[id] = backup
        return backup
    }

    override fun findBackupById(id: UUID): DbBackup? = backups[id]
    override fun listBackups(databaseId: UUID): List<DbBackup> =
        backups.values.filter { it.databaseId == databaseId }.sortedByDescending { it.createdAt }

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
    ): DbBackup {
        val existing = backups[id]!!
        val updated = existing.copy(
            restoreStatus = restoreStatus,
            restoreTargetDatabaseId = restoreTargetDatabaseId ?: existing.restoreTargetDatabaseId,
            restoreStatusReason = restoreStatusReason,
            restoreCompletedAt = restoreCompletedAt ?: existing.restoreCompletedAt,
        )
        backups[id] = updated
        return updated
    }
}

private class MapProjectRepository(private val ids: Set<UUID>) : ProjectRepository {
    override fun create(name: String, slug: String) = error("unused")
    override fun findById(id: UUID) =
        if (id in ids) {
            forge.control.domain.Project(
                id = id,
                name = "p-$id",
                slug = "p-${id.toString().take(8)}",
                createdAt = Instant.now(),
                updatedAt = Instant.now(),
            )
        } else {
            null
        }

    override fun findBySlug(slug: String) = null
    override fun list() = emptyList<forge.control.domain.Project>()
    override fun update(id: UUID, name: String?, slug: String?) = error("unused")
    override fun delete(id: UUID) = Unit
}

private object EmptyApplicationRepository : ApplicationRepository {
    override fun create(projectId: UUID, name: String) = error("unused")
    override fun findById(id: UUID) = null
    override fun findByProjectAndName(projectId: UUID, name: String) = null
    override fun list(projectId: UUID) = emptyList<forge.control.domain.Application>()
    override fun update(id: UUID, name: String) = error("unused")
    override fun delete(id: UUID) = Unit
}
