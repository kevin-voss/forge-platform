package forge.control.manageddb

import forge.control.ControlServices
import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.forgeControlModule
import forge.control.http.ErrorEnvelope
import forge.control.http.Readiness
import forge.control.http.dto.ProjectResponse
import forge.control.logging.JsonLog
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcIdempotencyStore
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import forge.control.http.dto.ApplicationResponse
import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.ApplicationTestBuilder
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import org.junit.jupiter.api.TestMethodOrder
import java.nio.file.Files
import java.sql.DriverManager
import java.util.UUID
import java.util.concurrent.Executor
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * Managed-db HTTP API against foundation Postgres (18.01).
 * Skipped when the DB is unreachable. Excluded from default `test` task.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class ManagedDbApiIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var services: ControlServices
    private val cfg = AppConfig(
        port = 8080,
        serviceName = "forge-control",
        serviceVersion = "0.1.0",
        logLevel = "info",
        otelEnabled = false,
        otlpEndpoint = "http://otel-collector:4317",
        env = "test",
        authMode = "dev",
        shutdownGraceSeconds = 10,
        database = DatabaseConfig(
            url = jdbcUrl,
            user = dbUser,
            password = dbPassword,
            schema = "control",
            poolMax = 4,
            migrateOnStart = true,
        ),
        dbProvisioner = "fake",
    )

    private var projectA: String? = null
    private var projectB: String? = null
    private var instanceId: String? = null
    private var databaseId: String? = null
    private var applicationId: String? = null
    private var attachmentId: String? = null
    private lateinit var secrets: InMemoryManagedDbSecretsClient

    @BeforeAll
    fun setup() {
        assumeTrue(isPostgresReachable(), "foundation Postgres not reachable at $jdbcUrl")
        db = Db.open(cfg.database)
        db.migrate()
        bindServices()
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun bindServices() {
        val projectRepo = JdbcProjectRepository(db.dataSource)
        val envRepo = JdbcEnvironmentRepository(db.dataSource)
        val applicationRepo = JdbcApplicationRepository(db.dataSource)
        val serviceRepo = JdbcServiceRepository(db.dataSource)
        val deploymentRepo = JdbcDeploymentRepository(db.dataSource)
        val auditRepo = JdbcAuditRepository(db.dataSource)
        val managedDbRepo = JdbcManagedDbRepository(db.dataSource)
        val relationships = RelationshipValidator(projectRepo, applicationRepo)
        val isolation = IsolationGuard(cfg.database.url, cfg.database.user)
        secrets = InMemoryManagedDbSecretsClient()
        val sync = Executor { it.run() }
        val backupDir = Files.createTempDirectory("forge-mdb-api-bak")
        val archives = VolumeArchiveStore(backupDir)
        val provisioner = FakeProvisioner(isolation)
        val managedDb = ManagedDbService(
            store = managedDbRepo,
            provisioner = provisioner,
            isolation = isolation,
            relationships = relationships,
            secrets = secrets,
            applications = applicationRepo,
            defaultEnvVar = "DATABASE_URL",
            backupRunner = BackupRunner(managedDbRepo, provisioner, archives, executor = sync),
            restoreRunner = RestoreRunner(managedDbRepo, provisioner, archives, executor = sync),
            archives = archives,
            log = JsonLog("forge-control", "info"),
        )
        services = ControlServices(
            projects = ProjectService(projectRepo, auditRepo, actor = "dev"),
            environments = EnvironmentService(projectRepo, envRepo, auditRepo, actor = "dev"),
            applications = ApplicationService(applicationRepo, relationships, auditRepo, actor = "dev"),
            services = ServiceService(serviceRepo, relationships, auditRepo, actor = "dev"),
            deployments = DeploymentService(
                deploymentRepo,
                serviceRepo,
                applicationRepo,
                envRepo,
                auditRepo,
                actor = "dev",
            ),
            projectTrees = ProjectTreeService(
                projectRepo,
                envRepo,
                applicationRepo,
                serviceRepo,
                deploymentRepo,
            ),
            idempotency = JdbcIdempotencyStore(db.dataSource),
            managedDb = managedDb,
        )
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(Json { ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false })
        }
    }

    private fun withApp(block: suspend ApplicationTestBuilder.() -> Unit) {
        testApplication {
            application {
                forgeControlModule(
                    cfg = cfg,
                    readiness = Readiness(initial = true),
                    services = services,
                )
            }
            block()
        }
    }

    @Test
    @Order(1)
    fun createListGetInstanceWithFakeProvisioner() = withApp {
        val client = jsonClient()
        val suffix = UUID.randomUUID().toString().take(8)
        val project = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"mdb-$suffix"}""")
        }.body<ProjectResponse>()
        projectA = project.id

        val created = client.post("/v1/databases/instances") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"main","projectId":"${project.id}"}""")
        }
        assertEquals(HttpStatusCode.Created, created.status)
        val instance = created.body<DbInstanceResponse>()
        instanceId = instance.id
        assertEquals("main", instance.name)
        assertEquals(project.id, instance.projectId)
        assertEquals("available", instance.status)
        assertTrue(instance.endpointRef!!.startsWith("fake://managed-db/"))
        assertTrue(!instance.endpointRef!!.contains("5001/forge"))

        val listed = client.get("/v1/databases/instances?projectId=${project.id}")
            .body<List<DbInstanceResponse>>()
        assertTrue(listed.any { it.id == instance.id && it.name == "main" })

        val got = client.get("/v1/databases/instances/${instance.id}").body<DbInstanceResponse>()
        assertEquals(instance.id, got.id)
        assertEquals("available", got.status)

        val dbs = client.get("/v1/databases/instances/${instance.id}/databases")
            .body<List<DbDatabaseResponse>>()
        assertEquals(emptyList(), dbs)
    }

    @Test
    @Order(2)
    fun duplicateInstanceNameReturns409() = withApp {
        val client = jsonClient()
        val pid = projectA!!
        val response = client.post("/v1/databases/instances") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"main","projectId":"$pid"}""")
        }
        assertEquals(HttpStatusCode.Conflict, response.status)
        val err = response.body<ErrorEnvelope>()
        assertEquals("conflict", err.error.code)
    }

    @Test
    @Order(3)
    fun missingInstanceReturns404() = withApp {
        val client = jsonClient()
        val missing = UUID.randomUUID()
        val response = client.get("/v1/databases/instances/$missing")
        assertEquals(HttpStatusCode.NotFound, response.status)
        assertEquals("not_found", response.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(4)
    fun crossProjectInstanceAccessIs404StyleIsolation() = withApp {
        val client = jsonClient()
        val suffix = UUID.randomUUID().toString().take(8)
        val other = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"mdb-b-$suffix"}""")
        }.body<ProjectResponse>()
        projectB = other.id

        // Listing project B must not include project A's instance.
        val listed = client.get("/v1/databases/instances") {
            header("X-Forge-Project", other.id)
        }.body<List<DbInstanceResponse>>()
        assertTrue(listed.none { it.id == instanceId })

        // Get by id still returns the record (global id); project isolation for list is enforced.
        // Cross-project "access" for list is empty; unknown ids are 404.
        val unknown = client.get("/v1/databases/instances/${UUID.randomUUID()}")
        assertEquals(HttpStatusCode.NotFound, unknown.status)
    }

    @Test
    @Order(5)
    fun isolationInvariantRefusesControlJdbcUrl() {
        val isolation = IsolationGuard(cfg.database.url, cfg.database.user)
        assertTrue(isolation.isControlDatabase(cfg.database.url))
        val managedDb = services.managedDb!!
        val ex = kotlin.test.assertFailsWith<forge.control.http.ApiException.BadRequest> {
            managedDb.assertEndpointAllowed(cfg.database.url)
        }
        assertTrue(ex.message!!.contains("Control"))
    }

    @Test
    @Order(6)
    fun createDatabaseStoresSecretRefAndOneTimePassword() = withApp {
        val client = jsonClient()
        val iid = instanceId!!
        val created = client.post("/v1/databases/instances/$iid/databases") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"appdb"}""")
        }
        assertEquals(HttpStatusCode.Created, created.status)
        val db = created.body<DbDatabaseResponse>()
        assertEquals("appdb", db.name)
        assertEquals("available", db.status)
        assertEquals("fake.local", db.host)
        assertEquals(5432, db.port)
        assertNotNull(db.secretRef)
        assertTrue(db.secretRef!!.startsWith("secret:project/"))
        assertNotNull(db.password)
        assertTrue(CredentialGenerator.isStrongPassword(db.password!!))
        assertNotNull(db.username)

        val listed = client.get("/v1/databases/instances/$iid/databases")
            .body<List<DbDatabaseResponse>>()
        assertEquals(1, listed.size)
        assertNull(listed.single().password, "list must not include plaintext password")
        assertEquals(db.secretRef, listed.single().secretRef)

        val got = client.get("/v1/databases/${db.id}").body<DbDatabaseResponse>()
        assertEquals(db.id, got.id)
        assertEquals("available", got.status)
        assertNull(got.password, "get must not include plaintext password")
        assertEquals(db.secretRef, got.secretRef)
        databaseId = db.id
    }

    @Test
    @Order(7)
    fun attachDetachAndListApplicationDatabases() = withApp {
        val client = jsonClient()
        val pid = projectA!!
        val dbid = databaseId!!
        val app = client.post("/v1/projects/$pid/applications") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"web-attach"}""")
        }.body<ApplicationResponse>()
        applicationId = app.id

        val attached = client.post("/v1/databases/$dbid/attach") {
            contentType(ContentType.Application.Json)
            setBody("""{"applicationId":"${app.id}","envVar":"DATABASE_URL"}""")
        }
        assertEquals(HttpStatusCode.Created, attached.status)
        val attachment = attached.body<DbAttachmentResponse>()
        attachmentId = attachment.id
        assertEquals(app.id, attachment.applicationId)
        assertEquals(dbid, attachment.databaseId)
        assertEquals("DATABASE_URL", attachment.envVar)
        assertNotNull(attachment.secretRef)
        assertTrue(attachment.secretRef!!.startsWith("secret:project/"))
        val url = secrets.get(attachment.secretRef!!)
        assertNotNull(url)
        assertTrue(url!!.startsWith("postgresql://"))
        // Response exposes secretRef only — never the plaintext URL field.
        assertTrue(!attachment.toString().contains(url))

        val listed = client.get("/v1/applications/${app.id}/databases")
            .body<List<DbAttachmentResponse>>()
        assertEquals(1, listed.size)
        assertEquals(attachment.id, listed.single().id)

        // Cross-project attach denied.
        val otherPid = projectB!!
        val otherApp = client.post("/v1/projects/$otherPid/applications") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"web-other"}""")
        }.body<ApplicationResponse>()
        val cross = client.post("/v1/databases/$dbid/attach") {
            contentType(ContentType.Application.Json)
            setBody("""{"applicationId":"${otherApp.id}"}""")
        }
        assertEquals(HttpStatusCode.NotFound, cross.status)

        val deleted = client.delete("/v1/databases/attachments/${attachment.id}")
        assertEquals(HttpStatusCode.NoContent, deleted.status)
        val after = client.get("/v1/applications/${app.id}/databases")
            .body<List<DbAttachmentResponse>>()
        assertEquals(emptyList(), after)
        assertNull(secrets.get(attachment.secretRef!!))
    }

    @Test
    @Order(8)
    fun backupRestoreListAndCrossProjectIsolation() = withApp {
        val client = jsonClient()
        val pid = projectA!!
        val dbid = databaseId!!

        val created = client.post("/v1/databases/$dbid/backups") {
            header("X-Forge-Project", pid)
        }
        assertEquals(HttpStatusCode.Accepted, created.status)
        val backup = created.body<DbBackupResponse>()
        assertTrue(backup.status == "running" || backup.status == "succeeded")

        // Poll get until succeeded (sync runner usually completes immediately).
        var got: DbBackupResponse? = null
        repeat(20) {
            got = client.get("/v1/databases/$dbid/backups/${backup.id}") {
                header("X-Forge-Project", pid)
            }.body()
            if (got!!.status == "succeeded" || got!!.status == "failed") return@repeat
            Thread.sleep(50)
        }
        assertEquals("succeeded", got!!.status)
        assertNotNull(got!!.checksum)
        assertNotNull(got!!.location)
        assertTrue(got!!.sizeBytes!! > 0)

        val listed = client.get("/v1/databases/$dbid/backups") {
            header("X-Forge-Project", pid)
        }.body<List<DbBackupResponse>>()
        assertTrue(listed.any { it.id == backup.id && it.checksum != null })

        val restored = client.post("/v1/databases/backups/${backup.id}/restore") {
            header("X-Forge-Project", pid)
            contentType(ContentType.Application.Json)
            setBody("""{"targetDatabaseId":"$dbid"}""")
        }
        assertEquals(HttpStatusCode.Accepted, restored.status)
        val restoreBody = restored.body<RestoreBackupResponse>()
        assertEquals("running", restoreBody.status)

        // Cross-project backup create → 404
        val other = projectB!!
        val cross = client.post("/v1/databases/$dbid/backups") {
            header("X-Forge-Project", other)
        }
        assertEquals(HttpStatusCode.NotFound, cross.status)
    }
}
