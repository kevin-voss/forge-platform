package forge.identity

import forge.identity.config.Config
import forge.identity.config.DatabaseConfig
import forge.identity.db.Database
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.http.ErrorEnvelope
import forge.identity.org.OrgMembershipResponse
import forge.identity.org.OrgResponse
import forge.identity.project.ProjectMembershipResponse
import forge.identity.project.ProjectResponse
import forge.identity.user.UserMembershipsResponse
import forge.identity.user.UserResponse
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
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
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Tenancy persistence + HTTP APIs (09.02).
 * Requires foundation Postgres `forge_identity`; skipped when unreachable.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class TenancyTest {
    private val jdbcUrl = System.getenv("FORGE_IDENTITY_DB_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge_identity"

    private lateinit var db: Database
    private lateinit var tenancy: TenancyServices

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
    )

    @BeforeAll
    fun setup() {
        try {
            db = Database.open(dbConfig())
            db.migrate()
            tenancy = TenancyServices.from(db)
        } catch (_: Exception) {
            assumeTrue(false, "Postgres forge_identity unreachable at $jdbcUrl")
        }
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun uniqueEmail(prefix: String = "user"): String =
        "$prefix-${UUID.randomUUID()}@example.com"

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(Json { ignoreUnknownKeys = true })
        }
    }

    private fun withApp(block: suspend ApplicationTestBuilder.() -> Unit) {
        val probe = DbProbe { db.check() }
        testApplication {
            application {
                forgeIdentityModule(
                    cfg = cfg(),
                    readiness = Readiness(initial = true),
                    dbProbe = probe,
                    tenancy = tenancy,
                )
            }
            block()
        }
    }

    @Test
    fun duplicateEmailRejectedWith409() = withApp {
        val client = jsonClient()
        val email = uniqueEmail("dup")
        val first = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","display_name":"One"}""")
        }
        assertEquals(HttpStatusCode.Created, first.status)

        val second = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","display_name":"Two"}""")
        }
        assertEquals(HttpStatusCode.Conflict, second.status)
        val err = second.body<ErrorEnvelope>()
        assertEquals("conflict", err.error.code)
        assertTrue(err.error.requestId.isNotBlank())
        assertEquals(email, err.error.details?.get("email"))
    }

    @Test
    fun caseInsensitiveEmailUniqueness() = withApp {
        val client = jsonClient()
        val suffix = UUID.randomUUID().toString().take(8)
        val create = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"A@$suffix.com","display_name":"Case"}""")
        }
        assertEquals(HttpStatusCode.Created, create.status)

        val dup = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"a@$suffix.com","display_name":"Case2"}""")
        }
        assertEquals(HttpStatusCode.Conflict, dup.status)
    }

    @Test
    fun membershipOnMissingUserOrOrgErrors() = withApp {
        val client = jsonClient()
        val org = client.post("/v1/orgs") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Org-${UUID.randomUUID()}"}""")
        }.body<OrgResponse>()

        val missingUser = client.post("/v1/orgs/${org.id}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${UUID.randomUUID()}","role":"organization-owner"}""")
        }
        assertEquals(HttpStatusCode.NotFound, missingUser.status)

        val missingOrg = client.post("/v1/orgs/${UUID.randomUUID()}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${UUID.randomUUID()}","role":"organization-owner"}""")
        }
        assertEquals(HttpStatusCode.NotFound, missingOrg.status)

        val missingProject = client.post("/v1/projects/${UUID.randomUUID()}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${UUID.randomUUID()}","role":"developer"}""")
        }
        assertEquals(HttpStatusCode.NotFound, missingProject.status)
    }

    @Test
    fun duplicateMembershipIsIdempotent() = withApp {
        val client = jsonClient()
        val user = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"${uniqueEmail("idem")}","display_name":"Idem"}""")
        }.body<UserResponse>()
        val org = client.post("/v1/orgs") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"IdemOrg-${UUID.randomUUID()}"}""")
        }.body<OrgResponse>()

        val first = client.post("/v1/orgs/${org.id}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${user.id}","role":"organization-owner"}""")
        }
        assertEquals(HttpStatusCode.Created, first.status)
        val firstBody = first.body<OrgMembershipResponse>()

        val second = client.post("/v1/orgs/${org.id}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${user.id}","role":"organization-owner"}""")
        }
        assertEquals(HttpStatusCode.Created, second.status)
        val secondBody = second.body<OrgMembershipResponse>()
        assertEquals(firstBody, secondBody)

        val projectId = UUID.randomUUID().toString()
        client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"id":"$projectId","org_id":"${org.id}","name":"p"}""")
        }
        val p1 = client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${user.id}","role":"developer"}""")
        }.body<ProjectMembershipResponse>()
        val p2 = client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${user.id}","role":"developer"}""")
        }.body<ProjectMembershipResponse>()
        assertEquals(p1, p2)
    }

    @Test
    fun createUserOrgProjectMembershipsAggregation() = withApp {
        val client = jsonClient()
        val user = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            header("X-Request-Id", "req_tenancy_flow")
            setBody("""{"email":"${uniqueEmail("flow")}","display_name":"Flow"}""")
        }
        assertEquals(HttpStatusCode.Created, user.status)
        assertEquals("req_tenancy_flow", user.headers["X-Request-Id"])
        val userBody = user.body<UserResponse>()

        val org = client.post("/v1/orgs") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Acme-${UUID.randomUUID()}"}""")
        }.body<OrgResponse>()

        val orgMember = client.post("/v1/orgs/${org.id}/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${userBody.id}","role":"organization-owner"}""")
        }
        assertEquals(HttpStatusCode.Created, orgMember.status)

        val projectId = UUID.randomUUID().toString()
        val project = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"id":"$projectId","org_id":"${org.id}","name":"demo"}""")
        }.body<ProjectResponse>()
        assertEquals(projectId, project.id)

        val projectMember = client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${userBody.id}","role":"developer"}""")
        }
        assertEquals(HttpStatusCode.Created, projectMember.status)

        val memberships = client.get("/v1/users/${userBody.id}/memberships")
            .body<UserMembershipsResponse>()
        assertEquals(1, memberships.orgs.size)
        assertEquals(org.id, memberships.orgs[0].org_id)
        assertEquals("organization-owner", memberships.orgs[0].role)
        assertEquals(1, memberships.projects.size)
        assertEquals(projectId, memberships.projects[0].project_id)
        assertEquals("developer", memberships.projects[0].role)

        val removeOrg = client.delete("/v1/orgs/${org.id}/members/${userBody.id}")
        assertEquals(HttpStatusCode.NoContent, removeOrg.status)
        val removeProject = client.delete("/v1/projects/$projectId/members/${userBody.id}")
        assertEquals(HttpStatusCode.NoContent, removeProject.status)

        val after = client.get("/v1/users/${userBody.id}/memberships")
            .body<UserMembershipsResponse>()
        assertTrue(after.orgs.isEmpty())
        assertTrue(after.projects.isEmpty())
    }

    @Test
    fun getUserByEmailQuery() = withApp {
        val client = jsonClient()
        val email = uniqueEmail("lookup")
        val created = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","display_name":"Lookup"}""")
        }.body<UserResponse>()

        val listed: List<UserResponse> = client.get("/v1/users?email=$email").body()
        assertEquals(1, listed.size)
        assertEquals(created.id, listed[0].id)

        val missing: HttpResponse = client.get("/v1/users?email=missing-${UUID.randomUUID()}@x.com")
        assertEquals(HttpStatusCode.NotFound, missing.status)
    }
}
