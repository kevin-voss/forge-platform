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
import forge.control.scheduler.api.IssueBootstrapTokenResponse
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
import io.ktor.client.request.delete
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
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * Join handshake HTTP integration tests. Skipped when Postgres is unreachable.
 * Excluded from default `test` task (see build.gradle.kts).
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class NodeJoinApiIntegrationTest {
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
        networkUrl = "http://forge-network:4110",
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
        val tokens = JdbcBootstrapTokenStore(db.dataSource)
        val network = object : NetworkClient {
            override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult =
                NetworkLeaseResult.Ok(
                    NodeNetworkLease(nodeId, "10.100.1.0/24", "10.100.1.1"),
                )
        }
        val join = NodeJoinOrchestrator(
            nodes = nodeStore,
            tokens = tokens,
            network = network,
            log = log,
            requireTokenWhenNetworkConfigured = true,
        )
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
            bootstrapTokenStore = tokens,
            nodeJoinOrchestrator = join,
        )
    }

    @Test
    fun fullJoinIssueRegisterHeartbeatOnline() = testApplication {
        purge()
        application {
            forgeControlModule(cfg, Readiness().also { it.markReady() }, services = services, log = log)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true; encodeDefaults = true })
            }
        }

        val issued = client.post("/v1/nodes/bootstrap-tokens") {
            contentType(ContentType.Application.Json)
            setBody("""{"organization":"forge-labs","ttl_seconds":900}""")
        }
        assertEquals(HttpStatusCode.Created, issued.status)
        val tokenBody: IssueBootstrapTokenResponse = issued.body()
        assertTrue(tokenBody.token.startsWith("bst_"))

        val reg = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {
                  "node_id":"node-a",
                  "address":"http://runtime-a:4102",
                  "capacity":{"slots":4},
                  "bootstrap_token":"${tokenBody.token}",
                  "wireguard_public_key":"b64:test"
                }
                """.trimIndent(),
            )
        }
        assertEquals(HttpStatusCode.Created, reg.status)
        val joined: NodeResponse = reg.body()
        assertEquals("joining", joined.status)
        assertNotNull(joined.network)
        assertEquals("10.100.1.0/24", joined.network!!.cidr)

        val reused = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {
                  "node_id":"node-z",
                  "address":"http://runtime-z:4102",
                  "capacity":{"slots":4},
                  "bootstrap_token":"${tokenBody.token}",
                  "wireguard_public_key":"b64:test2"
                }
                """.trimIndent(),
            )
        }
        assertEquals(HttpStatusCode.Unauthorized, reused.status)

        val hb = client.post("/v1/nodes/node-a/heartbeat") {
            contentType(ContentType.Application.Json)
            setBody("""{"allocated":{"slots":0},"free":{"slots":4},"running_replicas":[]}""")
        }
        assertEquals(HttpStatusCode.OK, hb.status)
        val online: NodeResponse = hb.body()
        assertEquals("online", online.status)

        // Resume without token
        val resume = client.post("/v1/nodes/register") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"node_id":"node-a","address":"http://runtime-a:4102","capacity":{"slots":4}}""",
            )
        }
        assertEquals(HttpStatusCode.OK, resume.status)
        assertEquals("online", resume.body<NodeResponse>().status)

        val revoked = client.post("/v1/nodes/node-a/revoke-key")
        assertEquals(HttpStatusCode.OK, revoked.status)

        val hbAfter = client.post("/v1/nodes/node-a/heartbeat") {
            contentType(ContentType.Application.Json)
            setBody("""{"allocated":{"slots":0},"free":{"slots":4}}""")
        }
        assertEquals(HttpStatusCode.Unauthorized, hbAfter.status)

        client.delete("/v1/nodes/bootstrap-tokens/${tokenBody.id}")
            .also { assertTrue(it.status == HttpStatusCode.NoContent || it.status == HttpStatusCode.OK) }
    }

    private fun purge() {
        db.dataSource.connection.use { conn ->
            conn.prepareStatement("DELETE FROM bootstrap_tokens").use { it.executeUpdate() }
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
