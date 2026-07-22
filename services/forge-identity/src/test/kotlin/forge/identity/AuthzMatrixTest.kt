package forge.identity

import forge.identity.authz.AuthzCheckResponse
import forge.identity.authz.AuthzMatrixResponse
import forge.identity.authz.AuthzService
import forge.identity.authz.PermissionMatrix
import forge.identity.authz.Role
import forge.identity.config.Config
import forge.identity.config.DatabaseConfig
import forge.identity.db.Database
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.http.ErrorEnvelope
import forge.identity.org.OrgResponse
import forge.identity.project.ProjectResponse
import forge.identity.user.UserResponse
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
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import java.nio.file.Files
import java.nio.file.Path
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * Role model + permission matrix + authz/check (09.04).
 * HTTP cases need foundation Postgres `forge_identity`; skipped when unreachable.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class AuthzMatrixTest {
    private val jdbcUrl = System.getenv("FORGE_IDENTITY_DB_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge_identity"

    private lateinit var db: Database
    private lateinit var tenancy: TenancyServices
    private lateinit var authz: AuthzService

    private val matrix = PermissionMatrix.default()

    private fun dbConfig() = DatabaseConfig(
        url = jdbcUrl,
        user = System.getenv("FORGE_IDENTITY_DB_USER") ?: "forge",
        password = System.getenv("FORGE_IDENTITY_DB_PASSWORD") ?: "forge",
        poolMax = 2,
        migrateOnStart = true,
        connectRetryInitialMs = 200,
        connectRetryMaxMs = 1000,
    )

    private fun cfg() = Config(
        port = 4002,
        serviceName = "forge-identity",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
        shutdownGraceSeconds = 10,
        database = dbConfig(),
        authzMatrixVersion = matrix.version,
    )

    @BeforeAll
    fun setup() {
        try {
            db = Database.open(dbConfig())
            db.migrate()
            tenancy = TenancyServices.from(db)
            authz = AuthzService.create(
                projects = tenancy.projects,
                orgs = tenancy.orgs,
                matrix = matrix,
            )
        } catch (_: Exception) {
            assumeTrue(false, "Postgres forge_identity unreachable at $jdbcUrl")
        }
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(Json { ignoreUnknownKeys = true })
        }
    }

    private fun withApp(block: suspend ApplicationTestBuilder.() -> Unit) {
        assumeTrue(::db.isInitialized)
        val probe = DbProbe { db.check() }
        testApplication {
            application {
                forgeIdentityModule(
                    cfg = cfg(),
                    readiness = Readiness(initial = true),
                    dbProbe = probe,
                    tenancy = tenancy,
                    authz = authz,
                )
            }
            block()
        }
    }

    @Test
    fun matrixViewerDeveloperAndServiceAccountRules() {
        assertFalse(matrix.allows("deployment.create", Role.VIEWER))
        assertTrue(matrix.allows("deployment.create", Role.DEVELOPER))
        assertTrue(matrix.allows("project.read", Role.VIEWER))
        assertTrue(matrix.allows("deployment.create", Role.SERVICE_ACCOUNT))
        assertFalse(matrix.allows("member.manage", Role.SERVICE_ACCOUNT))
        assertTrue(matrix.allows("member.manage", Role.ORGANIZATION_OWNER))
        assertTrue(matrix.allows("member.manage", Role.PROJECT_ADMIN))
        assertFalse(matrix.allows("member.manage", Role.DEVELOPER))
        assertFalse(matrix.allows("unknown.action", Role.DEVELOPER))
        assertFalse(matrix.allows("deployment.create", Role.NONE))
    }

    @Test
    fun exhaustiveRoleTimesRepresentativeActions() {
        val representative = listOf(
            "project.read",
            "deployment.create",
            "secret.write",
            "member.manage",
        )
        for (role in Role.membershipRoles) {
            for (action in representative) {
                val expected = matrix.allowedRoles(action)!!.contains(role)
                assertEquals(
                    expected,
                    matrix.allows(action, role),
                    "role=${role.wire} action=$action",
                )
            }
        }
        for (action in matrix.knownActions()) {
            assertFalse(matrix.allows(action, Role.NONE), "none must be denied for $action")
        }
    }

    @Test
    fun publishedDocMatchesCodeMatrix() {
        val root = System.getenv("FORGE_ROOT")?.let { Path.of(it) }
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.firstOrNull {
                    Files.exists(it.resolve("docs/contracts"))
                }
            }
        assumeTrue(root != null, "docs/contracts not available in this build context")
        val docPath = root!!.resolve("docs/contracts/authz-permission-matrix.md")
        assertTrue(Files.exists(docPath), "missing $docPath")
        val text = Files.readString(docPath)
        val start = text.indexOf("```json")
        assertTrue(start >= 0, "doc must contain a ```json fence with the matrix")
        val jsonStart = text.indexOf('\n', start) + 1
        val end = text.indexOf("```", jsonStart)
        assertTrue(end > jsonStart, "unclosed json fence")
        val jsonText = text.substring(jsonStart, end).trim()
        val parsed = Json.parseToJsonElement(jsonText).jsonObject
        assertEquals(matrix.version, parsed["version"]!!.jsonPrimitive.content)
        val docMatrix = parsed["matrix"]!!.jsonObject
        val code = matrix.toWireMap()
        assertEquals(code.keys, docMatrix.keys)
        for ((action, roles) in code) {
            val docRoles = docMatrix[action]!!.jsonArray.map { it.jsonPrimitive.content }
            assertEquals(roles, docRoles, "parity mismatch for $action")
        }
    }

    @Test
    fun checkRequestResponseShapeMatchesOpenApiExamples() {
        val request = """
            {
              "principal": { "type": "user", "id": "usr_1" },
              "project_id": "prj_1",
              "action": "deployment.create"
            }
        """.trimIndent()
        val req = Json { ignoreUnknownKeys = true }
            .decodeFromString(forge.identity.authz.AuthzCheckRequest.serializer(), request)
        assertEquals("user", req.principal?.type)
        assertEquals("usr_1", req.principal?.id)
        assertEquals("prj_1", req.project_id)
        assertEquals("deployment.create", req.action)

        val response = """
            { "allow": true, "role": "developer", "reason": "developer may deployment.create" }
        """.trimIndent()
        val decoded = Json.decodeFromString(AuthzCheckResponse.serializer(), response)
        assertTrue(decoded.allow)
        assertEquals("developer", decoded.role)
    }

    @Test
    fun developerAllowedViewerDeniedAndOrgOwnerEscalation() = withApp {
        val client = jsonClient()
        val suffix = UUID.randomUUID().toString().take(8)

        val org = client.post("/v1/orgs") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Authz Org $suffix"}""")
        }.body<OrgResponse>()

        val owner = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"owner-$suffix@example.com","display_name":"Owner"}""")
        }.body<UserResponse>()
        client.post("/v1/orgs/${org.id}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${owner.id}","role":"organization-owner"}""")
        }

        val projectId = "prj-authz-$suffix"
        val project = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"id":"$projectId","org_id":"${org.id}","name":"Authz Project"}""")
        }.body<ProjectResponse>()
        assertEquals(projectId, project.id)

        val developer = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"dev-$suffix@example.com","display_name":"Dev"}""")
        }.body<UserResponse>()
        val viewer = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"view-$suffix@example.com","display_name":"View"}""")
        }.body<UserResponse>()
        val service = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"sa-$suffix@example.com","display_name":"SA"}""")
        }.body<UserResponse>()

        client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${developer.id}","role":"developer"}""")
        }
        client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${viewer.id}","role":"viewer"}""")
        }
        client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${service.id}","role":"service-account"}""")
        }

        val devAllow = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${developer.id}"},"project_id":"$projectId","action":"deployment.create"}""",
            )
        }.body<AuthzCheckResponse>()
        assertTrue(devAllow.allow)
        assertEquals("developer", devAllow.role)

        val viewDeny = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${viewer.id}"},"project_id":"$projectId","action":"deployment.create"}""",
            )
        }.body<AuthzCheckResponse>()
        assertFalse(viewDeny.allow)
        assertEquals("viewer", viewDeny.role)

        val viewRead = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${viewer.id}"},"project_id":"$projectId","action":"project.read"}""",
            )
        }.body<AuthzCheckResponse>()
        assertTrue(viewRead.allow)

        // Org-owner is not a direct project member but may manage members.
        val ownerManage = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${owner.id}"},"project_id":"$projectId","action":"member.manage"}""",
            )
        }.body<AuthzCheckResponse>()
        assertTrue(ownerManage.allow)
        assertEquals("organization-owner", ownerManage.role)

        val saDeploy = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${service.id}"},"project_id":"$projectId","action":"deployment.create"}""",
            )
        }.body<AuthzCheckResponse>()
        assertTrue(saDeploy.allow)
        assertEquals("service-account", saDeploy.role)

        val saMember = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${service.id}"},"project_id":"$projectId","action":"member.manage"}""",
            )
        }.body<AuthzCheckResponse>()
        assertFalse(saMember.allow)

        val unknown = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${developer.id}"},"project_id":"$projectId","action":"not.a.real.action"}""",
            )
        }.body<AuthzCheckResponse>()
        assertFalse(unknown.allow)
        assertEquals("unknown action", unknown.reason)

        val stranger = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"none-$suffix@example.com","display_name":"None"}""")
        }.body<UserResponse>()
        val noMembership = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${stranger.id}"},"project_id":"$projectId","action":"project.read"}""",
            )
        }.body<AuthzCheckResponse>()
        assertFalse(noMembership.allow)
        assertEquals("none", noMembership.role)

        val missingProject = client.post("/v1/authz/check") {
            contentType(ContentType.Application.Json)
            setBody(
                """{"principal":{"type":"user","id":"${developer.id}"},"project_id":"missing-$suffix","action":"project.read"}""",
            )
        }
        assertEquals(HttpStatusCode.NotFound, missingProject.status)
        val err = missingProject.body<ErrorEnvelope>()
        assertEquals("not_found", err.error.code)

        val published = client.get("/v1/authz/matrix").body<AuthzMatrixResponse>()
        assertEquals(matrix.version, published.version)
        assertEquals(matrix.toWireMap(), published.matrix)
        assertNotNull(published.matrix["deployment.create"])
        assertTrue(published.matrix["deployment.create"]!!.contains("developer"))
        assertFalse(published.matrix["deployment.create"]!!.contains("viewer"))
    }

    @Test
    fun openApiDeclaresAuthzPaths() {
        val root = System.getenv("FORGE_ROOT")?.let { Path.of(it) }
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.firstOrNull {
                    Files.exists(it.resolve("contracts"))
                }
            }
        assumeTrue(root != null, "contracts/ not available in this build context")
        val yaml = Files.readString(root!!.resolve("contracts/openapi/forge-identity.openapi.yaml"))
        assertTrue(yaml.contains("/v1/authz/check"))
        assertTrue(yaml.contains("/v1/authz/matrix"))
        assertTrue(yaml.contains("AuthzCheckResponse"))
        assertTrue(yaml.contains("operationId: checkAuthorization") || yaml.contains("checkAuthorization"))
    }
}
