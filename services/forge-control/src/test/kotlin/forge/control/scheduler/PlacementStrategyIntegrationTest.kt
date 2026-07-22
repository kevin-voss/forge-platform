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
import forge.control.scheduler.api.NodeResponse
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
import java.time.Instant
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Fleet placement strategy integration tests. Skipped when Postgres is unreachable.
 * Excluded from default `test` task (see build.gradle.kts).
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PlacementStrategyIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var nodeStore: JdbcNodeStore
    private lateinit var servicesLeast: ControlServices
    private lateinit var servicesFirstFit: ControlServices
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
        schedulerStrategy = "least-allocated",
    )

    @BeforeAll
    fun setup() {
        assumeTrue(isPostgresReachable(), "foundation Postgres not reachable at $jdbcUrl")
        db = Db.open(cfg.database)
        db.migrate()
        nodeStore = JdbcNodeStore(db.dataSource)
        servicesLeast = bindServices("least-allocated")
        servicesFirstFit = bindServices("first-fit")
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun bindServices(strategy: String): ControlServices {
        val projectRepo = JdbcProjectRepository(db.dataSource)
        val envRepo = JdbcEnvironmentRepository(db.dataSource)
        val applicationRepo = JdbcApplicationRepository(db.dataSource)
        val serviceRepo = JdbcServiceRepository(db.dataSource)
        val deploymentRepo = JdbcDeploymentRepository(db.dataSource)
        val auditRepo = JdbcAuditRepository(db.dataSource)
        val relationships = RelationshipValidator(projectRepo, applicationRepo)
        val placementStore = JdbcPlacementStore(db.dataSource)
        val reservation = CapacityReservation(nodeStore)
        val scheduler = SchedulerFactory.create(
            strategy = strategy,
            nodeStore = nodeStore,
            reservation = reservation,
            localNodeId = "node-local",
            schedulerEnabled = true,
        )
        val placementService = PlacementService(
            scheduler = scheduler,
            store = placementStore,
            log = log,
            reservation = reservation,
        )
        return ControlServices(
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
            nodeStore = nodeStore,
        )
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    private fun registerFleet(suffix: String) {
        // Isolate from leftover fleet rows: mark every previously online node offline.
        nodeStore.markStaleOffline(Instant.now().plusSeconds(86_400))
        val a = "node-a-$suffix"
        val b = "node-b-$suffix"
        nodeStore.register(a, "http://$a:4102", NodeCapacity(slots = 4))
        nodeStore.register(b, "http://$b:4102", NodeCapacity(slots = 4))
        // Ensure allocation starts at zero for deterministic placement.
        nodeStore.heartbeat(a, NodeAllocation(slots = 0))
        nodeStore.heartbeat(b, NodeAllocation(slots = 0))
    }

    @Test
    fun leastAllocatedSpreadsFourReplicasAndUpdatesAllocation() = testApplication {
        val suffix = UUID.randomUUID().toString().take(8)
        registerFleet(suffix)
        val nodeA = "node-a-$suffix"
        val nodeB = "node-b-$suffix"

        application {
            forgeControlModule(cfg, Readiness().also { it.markReady() }, services = servicesLeast)
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

        val deployment = seedDeployment(client)
        repeat(4) { i ->
            val created = client.post("/v1/placements") {
                contentType(ContentType.Application.Json)
                setBody(
                    """{"deployment_id":"${deployment.id}","replica_index":$i,"requirements":{"slots":1}}""",
                )
            }
            assertEquals(HttpStatusCode.Created, created.status)
            val placement = created.body<PlacementResponse>()
            assertEquals("least-allocated", placement.strategy)
            assertTrue(placement.nodeId == nodeA || placement.nodeId == nodeB)
        }

        val listed = client.get("/v1/placements?deployment=${deployment.id}")
            .body<List<PlacementResponse>>()
        val byNode = listed.groupBy { it.nodeId }
        assertEquals(2, byNode[nodeA]?.size)
        assertEquals(2, byNode[nodeB]?.size)

        val nodes = client.get("/v1/nodes").body<List<NodeResponse>>()
        assertEquals(2, nodes.first { it.id == nodeA }.allocated.slots)
        assertEquals(2, nodes.first { it.id == nodeB }.allocated.slots)
        assertEquals(2, nodes.first { it.id == nodeA }.free.slots)
        assertEquals(2, nodes.first { it.id == nodeB }.free.slots)
    }

    @Test
    fun firstFitFillsFirstNodeBeforeSecond() = testApplication {
        val suffix = UUID.randomUUID().toString().take(8)
        registerFleet(suffix)
        val nodeA = "node-a-$suffix"
        val nodeB = "node-b-$suffix"

        application {
            forgeControlModule(
                cfg.copy(schedulerStrategy = "first-fit"),
                Readiness().also { it.markReady() },
                services = servicesFirstFit,
            )
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

        val deployment = seedDeployment(client)
        val nodes = (0 until 4).map { i ->
            val created = client.post("/v1/placements") {
                contentType(ContentType.Application.Json)
                setBody(
                    """{"deployment_id":"${deployment.id}","replica_index":$i,"requirements":{"slots":1}}""",
                )
            }
            assertEquals(HttpStatusCode.Created, created.status)
            val placement = created.body<PlacementResponse>()
            assertEquals("first-fit", placement.strategy)
            placement.nodeId
        }
        assertEquals(listOf(nodeA, nodeA, nodeA, nodeA), nodes)

        val fleet = client.get("/v1/nodes").body<List<NodeResponse>>()
        assertEquals(4, fleet.first { it.id == nodeA }.allocated.slots)
        assertEquals(0, fleet.first { it.id == nodeB }.allocated.slots)
    }

    private suspend fun seedDeployment(
        client: io.ktor.client.HttpClient,
    ): DeploymentResponse {
        val project = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"sched-strat-${UUID.randomUUID()}"}""")
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
        return client.post("/v1/services/${service.id}/deployments") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"image":"registry.local/demo:v1","desiredReplicas":4,"environmentId":"${env.id}"}""",
            )
        }.body<DeploymentResponse>()
    }
}
