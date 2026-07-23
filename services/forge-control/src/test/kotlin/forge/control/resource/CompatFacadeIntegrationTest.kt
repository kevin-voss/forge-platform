package forge.control.resource

import forge.control.ControlServices
import forge.control.buildKindRegistry
import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.forgeControlModule
import forge.control.http.ErrorEnvelope
import forge.control.http.Readiness
import forge.control.http.dto.ApplicationResponse
import forge.control.http.dto.ProjectResponse
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
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
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
 * 20.07: legacy route parity + Application via apply / generic resource API.
 * Skipped when Postgres is unreachable. Excluded from default `test` task.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class CompatFacadeIntegrationTest {
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
        resourceApiEnabled = true,
    )

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false }
    private val slug = "compat-${UUID.randomUUID().toString().take(8)}"
    private val appName = "invoice-api"
    private val environment = "production"

    @BeforeAll
    fun setup() {
        assumeTrue(isPostgresReachable(), "foundation Postgres not reachable at $jdbcUrl")
        db = Db.open(cfg.database)
        assertTrue(db.migrate().success)
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
            resources = CompatibilityResourceRepository(
                jdbc = JdbcResourceRepository(db.dataSource),
                projects = projectRepo,
                environments = envRepo,
                applications = applicationRepo,
                services = serviceRepo,
                deployments = deploymentRepo,
                audit = auditRepo,
                actor = "dev",
            ),
            resourceEvents = JdbcResourceEventRepository(db.dataSource),
            kindRegistry = buildKindRegistry(),
        )
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    private fun withApp(block: suspend io.ktor.server.testing.ApplicationTestBuilder.() -> Unit) {
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
    fun legacyProjectCreateShapeUnchanged() = withApp {
        val client = createClient {
            install(ContentNegotiation) { json(json) }
        }
        val response = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody(buildJsonObject {
                put("name", JsonPrimitive("Compat $slug"))
                put("slug", JsonPrimitive(slug))
            })
        }
        assertEquals(HttpStatusCode.Created, response.status)
        val body = response.body<ProjectResponse>()
        assertEquals(slug, body.slug)
        assertTrue(body.id.isNotBlank())
        assertTrue(body.createdAt.isNotBlank())
    }

    @Test
    @Order(2)
    fun dryRunDoesNotMutate() = withApp {
        val client = createClient {
            install(ContentNegotiation) { json(json) }
        }
        val before = countApplications(slug)
        val response = client.post("/v1/apply") {
            contentType(ContentType.Application.Json)
            setBody(
                ApplyRequest(
                    dryRun = true,
                    resources = listOf(
                        ResourceWriteRequest(
                            apiVersion = "forge.dev/v1",
                            kind = "Application",
                            metadata = ResourceMetadataWrite(
                                name = appName,
                                project = slug,
                                environment = environment,
                            ),
                            spec = buildJsonObject {
                                put("image", JsonPrimitive("registry.forge.internal/invoice-api:1.0.0"))
                            },
                        ),
                    ),
                ),
            )
        }
        assertEquals(HttpStatusCode.OK, response.status)
        val body = response.body<ApplyResponse>()
        assertTrue(body.dryRun)
        assertEquals(1, body.changedCount)
        assertEquals("create", body.results.single().action)
        assertEquals(before, countApplications(slug))
    }

    @Test
    @Order(3)
    fun applyCreatesApplicationVisibleOnGenericAndLegacyApis() = withApp {
        val client = createClient {
            install(ContentNegotiation) { json(json) }
        }
        // Ensure project exists (from order 1) and environment for addressing.
        client.post("/v1/apply") {
            contentType(ContentType.Application.Json)
            setBody(
                ApplyRequest(
                    dryRun = false,
                    resources = listOf(
                        ResourceWriteRequest(
                            apiVersion = "forge.dev/v1",
                            kind = "Environment",
                            metadata = ResourceMetadataWrite(name = environment, project = slug),
                            spec = buildJsonObject {},
                        ),
                        ResourceWriteRequest(
                            apiVersion = "forge.dev/v1",
                            kind = "Application",
                            metadata = ResourceMetadataWrite(
                                name = appName,
                                project = slug,
                                environment = environment,
                            ),
                            spec = buildJsonObject {
                                put("image", JsonPrimitive("registry.forge.internal/invoice-api:1.0.0"))
                            },
                        ),
                    ),
                ),
            )
        }.also { assertEquals(HttpStatusCode.OK, it.status) }

        val generic = client.get(
            "/v1/projects/$slug/environments/$environment/applications/$appName",
        )
        assertEquals(HttpStatusCode.OK, generic.status)
        val envelope = generic.body<ResourceEnvelopeResponse>()
        assertEquals("Application", envelope.kind)
        assertEquals(appName, envelope.metadata.name)
        assertEquals(slug, envelope.metadata.project)
        assertTrue(UUID.fromString(envelope.metadata.id).toString() == envelope.metadata.id)

        val projectId = services.projects.list().first { it.slug == slug }.id
        val legacyList = client.get("/v1/projects/$projectId/applications")
        assertEquals(HttpStatusCode.OK, legacyList.status)
        val apps = legacyList.body<List<ApplicationResponse>>()
        assertTrue(apps.any { it.name == appName })
    }

    @Test
    @Order(4)
    fun applyRejectsPortableViolationBeforeMutation() = withApp {
        val client = createClient {
            install(ContentNegotiation) { json(json) }
        }
        val before = countApplications(slug)
        val response = client.post("/v1/apply") {
            contentType(ContentType.Application.Json)
            setBody(
                ApplyRequest(
                    dryRun = false,
                    resources = listOf(
                        ResourceWriteRequest(
                            apiVersion = "forge.dev/v1",
                            kind = "Application",
                            metadata = ResourceMetadataWrite(
                                name = "bad-app",
                                project = slug,
                                environment = environment,
                            ),
                            spec = buildJsonObject {
                                put("provider", JsonPrimitive("aws"))
                            },
                        ),
                        ResourceWriteRequest(
                            apiVersion = "forge.dev/v1",
                            kind = "Application",
                            metadata = ResourceMetadataWrite(
                                name = "good-app",
                                project = slug,
                                environment = environment,
                            ),
                            spec = buildJsonObject {
                                put("image", JsonPrimitive("registry.forge.internal/ok:1"))
                            },
                        ),
                    ),
                ),
            )
        }
        assertEquals(HttpStatusCode.BadRequest, response.status)
        val err = response.body<ErrorEnvelope>()
        assertEquals("portable_manifest_violation", err.error.code)
        assertEquals(before, countApplications(slug))
    }

    @Test
    @Order(5)
    fun shippedKindsRegistered() {
        val registry = buildKindRegistry()
        for (kind in listOf(
            "Organization", "Project", "Environment", "Application", "Service",
            "Deployment", "Revision", "Route", "Secret", "Config",
        )) {
            assertTrue(registry.get(kind) != null, "missing kind $kind")
        }
    }

    private fun countApplications(projectSlug: String): Int {
        val project = JdbcProjectRepository(db.dataSource).findBySlug(projectSlug) ?: return 0
        return JdbcApplicationRepository(db.dataSource).list(project.id).size
    }
}
