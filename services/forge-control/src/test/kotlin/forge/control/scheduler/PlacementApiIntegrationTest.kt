package forge.control.scheduler

import forge.control.ControlServices
import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.forgeControlModule
import forge.control.http.Readiness
import forge.control.http.dto.ApplicationResponse
import forge.control.http.dto.DeploymentResponse
import forge.control.http.dto.EnvironmentResponse
import forge.control.http.dto.ProjectResponse
import forge.control.http.dto.ServiceResponse
import forge.control.logging.JsonLog
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcIdempotencyStore
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.scheduler.api.PlacementResponse
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import java.sql.DriverManager
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Placement HTTP + JDBC integration tests. Skipped when Postgres is unreachable.
 * Excluded from default `test` task (see build.gradle.kts).
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PlacementApiIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var services: ControlServices
    private val log = JsonLog("forge-control-test", "error")
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
    )

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
        val relationships = RelationshipValidator(projectRepo, applicationRepo)
        val placementStore = JdbcPlacementStore(db.dataSource)
        val placementService = PlacementService(
            scheduler = SingleNodeScheduler("node-local"),
            store = placementStore,
            log = log,
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
            placementService = placementService,
        )
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    @Test
    fun postPersistsAndGetListsPlacement() = testApplication {
        application {
            forgeControlModule(cfg, Readiness().also { it.markReady() }, services = services)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(
                    Json {
                        encodeDefaults = true
                        ignoreUnknownKeys = true
                        explicitNulls = false
                    },
                )
            }
        }

        val project = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"sched-${UUID.randomUUID()}"}""")
        }.body<ProjectResponse>()
        val env = client.post("/v1/projects/${project.id}/environments") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"dev"}""")
        }.body<EnvironmentResponse>()
        val app = client.post("/v1/projects/${project.id}/applications") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"web"}""")
        }.body<ApplicationResponse>()
        val service = client.post("/v1/applications/${app.id}/services") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"api","port":8080}""")
        }.body<ServiceResponse>()
        val deployment = client.post("/v1/services/${service.id}/deployments") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"image":"registry.local/demo:v1","desiredReplicas":1,"environmentId":"${env.id}"}""",
            )
        }.body<DeploymentResponse>()

        val created = client.post("/v1/placements") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"deployment_id":"${deployment.id}","replica_index":0,"requirements":{"slots":1}}""",
            )
        }
        assertEquals(HttpStatusCode.Created, created.status)
        val placement = created.body<PlacementResponse>()
        assertEquals("node-local", placement.nodeId)
        assertEquals("single-node", placement.strategy)
        assertEquals(0, placement.replicaIndex)
        assertEquals(deployment.id, placement.deploymentId)

        val listed = client.get("/v1/placements?deployment=${deployment.id}")
            .body<List<PlacementResponse>>()
        assertTrue(listed.size >= 1)
        assertEquals(placement.placementId, listed.first().placementId)

        val again = client.post("/v1/placements") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"deployment_id":"${deployment.id}","replica_index":0,"requirements":{"slots":1}}""",
            )
        }
        assertEquals(HttpStatusCode.OK, again.status)
        assertEquals(placement.placementId, again.body<PlacementResponse>().placementId)
    }
}
