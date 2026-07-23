package forge.control.manageddb

import forge.control.domain.Application
import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.ApplicationRepository
import forge.control.repo.RepositoryException
import forge.control.service.RelationshipValidator
import java.io.ByteArrayOutputStream
import java.io.PrintStream
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ManagedDbAttachTest {
    private val projectA = UUID.randomUUID()
    private val projectB = UUID.randomUUID()
    private val instanceId = UUID.randomUUID()
    private val databaseId = UUID.randomUUID()
    private val appA = UUID.randomUUID()
    private val appB = UUID.randomUUID()

    @Test
    fun attachDefaultsEnvVarAndStoresSecretRefOnly() {
        val secrets = InMemoryManagedDbSecretsClient()
        val password = "pw-attach-1"
        val credRef = secrets.putSecret(projectA, "managed-db-$databaseId", password)
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential(credRef),
        )
        val apps = FakeApplications(
            Application(appA, projectA, "web", Instant.now(), Instant.now()),
        )
        val svc = service(store, secrets, apps)

        val attachment = svc.attach(databaseId, appA.toString(), null)
        assertEquals("DATABASE_URL", attachment.envVar)
        assertNotNull(attachment.secretRef)
        assertTrue(attachment.secretRef!!.startsWith("secret:project/"))
        val url = secrets.get(attachment.secretRef!!)
        assertNotNull(url)
        assertTrue(url!!.startsWith("postgresql://"))
        assertTrue(url.contains(password))
        assertTrue(url.contains("fake.local:5432/appdb"))
    }

    @Test
    fun attachAcceptsEnvVarOverride() {
        val secrets = InMemoryManagedDbSecretsClient()
        val credRef = secrets.putSecret(projectA, "cred", "pw")
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential(credRef),
        )
        val apps = FakeApplications(
            Application(appA, projectA, "web", Instant.now(), Instant.now()),
        )
        val attachment = service(store, secrets, apps)
            .attach(databaseId, appA.toString(), "APP_DATABASE_URL")
        assertEquals("APP_DATABASE_URL", attachment.envVar)
    }

    @Test
    fun crossProjectAttachDeniedAsNotFound() {
        val secrets = InMemoryManagedDbSecretsClient()
        val credRef = secrets.putSecret(projectA, "cred", "pw")
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential(credRef),
        )
        val apps = FakeApplications(
            Application(appB, projectB, "other", Instant.now(), Instant.now()),
        )
        val ex = assertFailsWith<ApiException.NotFound> {
            service(store, secrets, apps).attach(databaseId, appB.toString(), null)
        }
        assertTrue(ex.message!!.contains("application"))
    }

    @Test
    fun resolveForApplicationInjectsEnvAndDetachRemovesIt() {
        val secrets = InMemoryManagedDbSecretsClient()
        val credRef = secrets.putSecret(projectA, "cred", "s3cret")
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential(credRef),
        )
        val apps = FakeApplications(
            Application(appA, projectA, "web", Instant.now(), Instant.now()),
        )
        val svc = service(store, secrets, apps)
        val attachment = svc.attach(databaseId, appA.toString(), null)

        val ready = svc.resolveForApplication(appA.toString()) as AttachmentEnvResult.Ready
        assertTrue(ready.env.containsKey("DATABASE_URL"))
        assertTrue(ready.env["DATABASE_URL"]!!.contains("s3cret"))
        assertTrue(ready.fingerprint.contains(attachment.secretRef!!))

        svc.detach(attachment.id)
        assertEquals(AttachmentEnvResult.Empty, svc.resolveForApplication(appA.toString()))
        assertNull(secrets.get(attachment.secretRef!!))
    }

    @Test
    fun attachLogsNeverContainPlaintextUrl() {
        val secrets = InMemoryManagedDbSecretsClient()
        val password = "super-secret-password-xyz"
        val credRef = secrets.putSecret(projectA, "cred", password)
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential(credRef),
        )
        val apps = FakeApplications(
            Application(appA, projectA, "web", Instant.now(), Instant.now()),
        )
        val buf = ByteArrayOutputStream()
        val prev = System.out
        System.setOut(PrintStream(buf))
        try {
            val log = JsonLog("forge-control-test", "info")
            val svc = service(store, secrets, apps, log)
            svc.attach(databaseId, appA.toString(), null)
        } finally {
            System.setOut(prev)
        }
        val logged = buf.toString()
        assertTrue(logged.contains("managed db attached"))
        assertTrue(!logged.contains(password), "password leaked in logs")
        assertTrue(!logged.contains("postgresql://"), "connection URL leaked in logs")
    }

    @Test
    fun missingAttachmentSecretHoldsResolve() {
        val secrets = InMemoryManagedDbSecretsClient()
        val store = FakeManagedDbRepository(
            instance = availableInstance(projectA),
            database = availableDatabase(),
            credential = activeCredential("secret:missing"),
        )
        store.attachments[UUID.randomUUID()] = DbAttachment(
            id = UUID.randomUUID(),
            databaseId = databaseId,
            applicationId = appA,
            envVar = "DATABASE_URL",
            secretRef = "secret:project/$projectA/env/managed-db/name/missing",
            createdAt = Instant.now(),
        )
        val apps = FakeApplications(
            Application(appA, projectA, "web", Instant.now(), Instant.now()),
        )
        val hold = service(store, secrets, apps).resolveForApplication(appA.toString())
        assertTrue(hold is AttachmentEnvResult.Hold)
    }

    private fun service(
        store: FakeManagedDbRepository,
        secrets: ManagedDbSecretsClient,
        apps: ApplicationRepository,
        log: JsonLog? = null,
    ): ManagedDbService {
        val isolation = IsolationGuard("jdbc:postgresql://127.0.0.1:5001/forge", "forge")
        return ManagedDbService(
            store = store,
            provisioner = FakeProvisioner(isolation),
            isolation = isolation,
            relationships = RelationshipValidator(
                projects = object : forge.control.repo.ProjectRepository {
                    override fun create(name: String, slug: String) = error("unused")
                    override fun findById(id: UUID) =
                        forge.control.domain.Project(id, "p", "p", Instant.now(), Instant.now())
                    override fun list() = emptyList<forge.control.domain.Project>()
                    override fun update(id: UUID, name: String?, slug: String?) = error("unused")
                    override fun delete(id: UUID) = error("unused")
                },
                applications = apps,
            ),
            secrets = secrets,
            applications = apps,
            defaultEnvVar = "DATABASE_URL",
            log = log,
        )
    }

    private fun availableInstance(projectId: UUID) = DbInstance(
        id = instanceId,
        projectId = projectId,
        name = "main",
        status = DbInstanceStatus.Available,
        host = "fake.local",
        port = 5432,
        containerId = "c1",
        endpointRef = "fake://managed-db/$instanceId",
        createdAt = Instant.now(),
        updatedAt = Instant.now(),
    )

    private fun availableDatabase() = DbDatabase(
        id = databaseId,
        instanceId = instanceId,
        name = "appdb",
        status = DbDatabaseStatus.Available,
        createdAt = Instant.now(),
    )

    private fun activeCredential(secretRef: String) = DbCredential(
        id = UUID.randomUUID(),
        databaseId = databaseId,
        username = "appdb_user",
        secretRef = secretRef,
        status = "active",
        createdAt = Instant.now(),
    )

    private class FakeApplications(vararg apps: Application) : ApplicationRepository {
        private val byId = apps.associateBy { it.id }
        override fun create(projectId: UUID, name: String) = error("unused")
        override fun findById(id: UUID) = byId[id]
        override fun list(projectId: UUID) = byId.values.filter { it.projectId == projectId }
        override fun update(id: UUID, name: String) = error("unused")
        override fun delete(id: UUID) = error("unused")
    }

    private class FakeManagedDbRepository(
        private val instance: DbInstance,
        private val database: DbDatabase,
        private val credential: DbCredential,
    ) : ManagedDbRepository {
        val attachments = ConcurrentHashMap<UUID, DbAttachment>()

        override fun createInstance(
            projectId: UUID,
            name: String,
            status: DbInstanceStatus,
            engine: String,
            deletionProtection: Boolean,
        ) = error("unused")

        override fun findInstanceById(id: UUID) = instance.takeIf { it.id == id }
        override fun listInstances(projectId: UUID) = listOf(instance).filter { it.projectId == projectId }
        override fun updateInstanceStatus(
            id: UUID,
            status: DbInstanceStatus,
            statusReason: String?,
            endpointRef: String?,
            host: String?,
            port: Int?,
            containerId: String?,
        ) = error("unused")

        override fun listDatabases(instanceId: UUID) = listOf(database)
        override fun findDatabaseById(id: UUID) = database.takeIf { it.id == id }
        override fun createDatabase(instanceId: UUID, name: String, status: DbDatabaseStatus) = error("unused")
        override fun updateDatabaseStatus(id: UUID, status: DbDatabaseStatus, statusReason: String?) = error("unused")
        override fun createCredential(
            databaseId: UUID,
            username: String,
            secretRef: String?,
            status: String,
        ) = error("unused")

        override fun findActiveCredential(databaseId: UUID) =
            credential.takeIf { it.databaseId == databaseId }

        override fun createAttachment(
            databaseId: UUID,
            applicationId: UUID,
            envVar: String,
            secretRef: String?,
            id: UUID,
        ): DbAttachment {
            if (attachments.values.any { it.databaseId == databaseId && it.applicationId == applicationId }) {
                throw RepositoryException.Conflict("unique")
            }
            val row = DbAttachment(id, databaseId, applicationId, envVar, secretRef, Instant.now())
            attachments[id] = row
            return row
        }

        override fun findAttachmentById(id: UUID) = attachments[id]
        override fun listAttachmentsByApplication(applicationId: UUID) =
            attachments.values.filter { it.applicationId == applicationId }

        override fun deleteAttachment(id: UUID) {
            if (attachments.remove(id) == null) throw RepositoryException.NotFound("db_attachment", id)
        }

        override fun deleteDatabase(id: UUID) = error("unused")
        override fun deleteCredential(id: UUID) = error("unused")

        override fun createBackup(databaseId: UUID, status: DbBackupStatus, id: UUID) = error("unused")
        override fun findBackupById(id: UUID): DbBackup? = null
        override fun listBackups(databaseId: UUID): List<DbBackup> = emptyList()
        override fun updateBackup(
            id: UUID,
            status: DbBackupStatus,
            location: String?,
            checksum: String?,
            sizeBytes: Long?,
            statusReason: String?,
            completedAt: Instant?,
        ) = error("unused")

        override fun updateBackupRestore(
            id: UUID,
            restoreStatus: DbRestoreStatus,
            restoreTargetDatabaseId: UUID?,
            restoreStatusReason: String?,
            restoreCompletedAt: Instant?,
        ) = error("unused")
    }
}
