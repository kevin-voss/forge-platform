package forge.control

import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.http.ErrorEnvelope
import forge.control.http.Readiness
import forge.control.http.dto.EnvironmentResponse
import forge.control.http.dto.ProjectResponse
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcProjectRepository
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
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
import java.sql.DriverManager
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * HTTP API integration tests against foundation Postgres.
 * Skipped when the DB is unreachable. Excluded from default `test` task.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class ProjectsEnvironmentsApiIntegrationTest {
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
    )

    private var createdProjectId: String? = null
    private var createdEnvironmentId: String? = null

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
        val auditRepo = JdbcAuditRepository(db.dataSource)
        services = ControlServices(
            projects = ProjectService(projectRepo, auditRepo, actor = "dev"),
            environments = EnvironmentService(projectRepo, envRepo, auditRepo, actor = "dev"),
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

    private fun withApi(block: suspend ApplicationTestBuilder.() -> Unit) = testApplication {
        application {
            forgeControlModule(cfg, Readiness(initial = true), services = services)
        }
        block()
    }

    @Test
    @Order(1)
    fun createAndGetProject() = withApi {
        val client = jsonClient()
        val slug = "api-${UUID.randomUUID()}"
        val create = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Acme","slug":"$slug"}""")
        }
        assertEquals(HttpStatusCode.Created, create.status)
        val project = create.body<ProjectResponse>()
        assertEquals("Acme", project.name)
        assertEquals(slug, project.slug)
        assertTrue(project.id.isNotBlank())
        createdProjectId = project.id

        val get = client.get("/v1/projects/${project.id}")
        assertEquals(HttpStatusCode.OK, get.status)
        assertEquals(project.id, get.body<ProjectResponse>().id)

        val list = client.get("/v1/projects")
        assertEquals(HttpStatusCode.OK, list.status)
        assertTrue(list.body<List<ProjectResponse>>().any { it.id == project.id })
    }

    @Test
    @Order(2)
    fun duplicateSlugAndBlankName() = withApi {
        val client = jsonClient()
        val slug = "dup-${UUID.randomUUID()}"
        val first = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"First","slug":"$slug"}""")
        }
        assertEquals(HttpStatusCode.Created, first.status)

        val dup = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Second","slug":"$slug"}""")
        }
        assertEquals(HttpStatusCode.Conflict, dup.status)
        assertEquals("conflict", dup.body<ErrorEnvelope>().error.code)

        val blank = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"  "}""")
        }
        assertEquals(HttpStatusCode.BadRequest, blank.status)
        assertEquals("invalid_request", blank.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(3)
    fun createListEnvironmentAndUnknownProject() = withApi {
        val client = jsonClient()
        val projectId = requireNotNull(createdProjectId)

        val create = client.post("/v1/projects/$projectId/environments") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"development"}""")
        }
        assertEquals(HttpStatusCode.Created, create.status)
        val env = create.body<EnvironmentResponse>()
        assertEquals("development", env.name)
        assertEquals(projectId, env.projectId)
        createdEnvironmentId = env.id

        val list = client.get("/v1/projects/$projectId/environments")
        assertEquals(HttpStatusCode.OK, list.status)
        assertTrue(list.body<List<EnvironmentResponse>>().any { it.id == env.id })

        val get = client.get("/v1/environments/${env.id}")
        assertEquals(HttpStatusCode.OK, get.status)
        assertEquals(env.id, get.body<EnvironmentResponse>().id)

        val dup = client.post("/v1/projects/$projectId/environments") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"development"}""")
        }
        assertEquals(HttpStatusCode.Conflict, dup.status)

        val unknown = client.post("/v1/projects/${UUID.randomUUID()}/environments") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"development"}""")
        }
        assertEquals(HttpStatusCode.NotFound, unknown.status)
        assertEquals("not_found", unknown.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(4)
    fun survivesReconnect() {
        val projectId = requireNotNull(createdProjectId)
        val envId = requireNotNull(createdEnvironmentId)

        db.close()
        db = Db.open(cfg.database)
        bindServices()

        assertEquals(projectId, services.projects.get(UUID.fromString(projectId)).id.toString())
        assertEquals(envId, services.environments.get(UUID.fromString(envId)).id.toString())

        testApplication {
            application {
                forgeControlModule(cfg, Readiness(initial = true), services = services)
            }
            val client = createClient {
                install(ContentNegotiation) {
                    json(Json { ignoreUnknownKeys = true })
                }
            }
            val response = client.get("/v1/projects/$projectId")
            assertEquals(HttpStatusCode.OK, response.status)
            assertEquals(projectId, response.body<ProjectResponse>().id)

            val envs = client.get("/v1/projects/$projectId/environments")
            assertEquals(HttpStatusCode.OK, envs.status)
            assertTrue(envs.body<List<EnvironmentResponse>>().any { it.id == envId })
        }
    }
}
