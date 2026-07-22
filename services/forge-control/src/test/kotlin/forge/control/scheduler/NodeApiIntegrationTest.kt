package forge.control.scheduler

import forge.control.ControlServices
import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.forgeControlModule
import forge.control.http.Readiness
import forge.control.logging.JsonLog
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcIdempotencyStore
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.scheduler.api.NodeResponse
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
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Node fleet HTTP + JDBC integration tests. Skipped when Postgres is unreachable.
 * Excluded from default `test` task (see build.gradle.kts).
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class NodeApiIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var services: ControlServices
    private lateinit var nodeStore: NodeStore
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
        nodeHeartbeatTimeoutSeconds = 15,
        livenessIntervalMs = 5_000,
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
        nodeStore = JdbcNodeStore(db.dataSource)
        val placementService = PlacementService(
            scheduler = SingleNodeScheduler("node-local"),
            store = JdbcPlacementStore(db.dataSource),
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
            nodeStore = nodeStore,
        )
    }

    @Test
    fun twoAgentsRegisterAndListOnlineWithCapacity() = testApplication {
        purgeNodes()
        application {
            forgeControlModule(cfg, Readiness().also { it.markReady() }, services = services, log = log)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true; encodeDefaults = true })
            }
        }

        val a = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"node_id":"node-a","address":"http://runtime-a:4102","capacity":{"slots":4}}""",
            )
        }
        assertEquals(HttpStatusCode.Created, a.status)
        val b = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"node_id":"node-b","address":"http://runtime-b:4102","capacity":{"slots":4}}""",
            )
        }
        assertEquals(HttpStatusCode.Created, b.status)

        // Idempotent re-register
        val again = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"node_id":"node-a","address":"http://runtime-a:4102","capacity":{"slots":4}}""",
            )
        }
        assertEquals(HttpStatusCode.OK, again.status)

        client.post("/v1/nodes/node-a/heartbeat") {
            contentType(ContentType.Application.Json)
            setBody("""{"allocated":{"slots":1},"free":{"slots":3},"running_replicas":["dpl:0"]}""")
        }.also { assertEquals(HttpStatusCode.OK, it.status) }

        val listed = client.get("/v1/nodes")
        assertEquals(HttpStatusCode.OK, listed.status)
        val nodes: List<NodeResponse> = listed.body()
        assertEquals(2, nodes.size)
        assertTrue(nodes.all { it.status == "online" })
        assertEquals(4, nodes.first { it.id == "node-a" }.capacity.slots)
        assertEquals(3, nodes.first { it.id == "node-a" }.free.slots)
        assertEquals(4, nodes.first { it.id == "node-b" }.capacity.slots)
    }

    @Test
    fun staleHeartbeatMarkedOfflineWhilePeerStaysOnline() {
        purgeNodes()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        nodeStore.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        nodeStore.register("node-b", "http://b", NodeCapacity(slots = 4), t0)
        nodeStore.heartbeat("node-a", NodeAllocation(slots = 0), t0.plusSeconds(20))

        val clock = object : Clock() {
            override fun getZone() = ZoneOffset.UTC
            override fun withZone(zone: java.time.ZoneId?) = this
            override fun instant() = t0.plusSeconds(20)
        }
        val monitor = LivenessMonitor(
            store = nodeStore,
            timeout = Duration.ofSeconds(15),
            intervalMs = 60_000,
            log = log,
            clock = clock,
        )
        monitor.evaluate()
        assertEquals("online", nodeStore.find("node-a")!!.status)
        assertEquals("offline", nodeStore.find("node-b")!!.status)
        // Row retained
        assertEquals(2, nodeStore.list().size)
    }

    @Test
    fun controlRestartRecomputesLivenessFromDb() {
        purgeNodes()
        val t0 = Instant.parse("2026-07-22T12:00:00Z")
        nodeStore.register("node-a", "http://a", NodeCapacity(slots = 4), t0)
        // Simulate restart: new monitor + recompute from persisted last_heartbeat_at
        val clock = object : Clock() {
            override fun getZone() = ZoneOffset.UTC
            override fun withZone(zone: java.time.ZoneId?) = this
            override fun instant() = t0.plusSeconds(30)
        }
        val monitor = LivenessMonitor(
            store = nodeStore,
            timeout = Duration.ofSeconds(15),
            intervalMs = 60_000,
            log = log,
            clock = clock,
        )
        monitor.evaluate()
        assertEquals("offline", nodeStore.find("node-a")!!.status)
        assertEquals(t0, nodeStore.find("node-a")!!.lastHeartbeatAt)
    }

    private fun purgeNodes() {
        db.dataSource.connection.use { conn ->
            conn.prepareStatement("DELETE FROM nodes").use { it.executeUpdate() }
        }
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }
}
