package forge.identity

import forge.identity.auth.IntrospectResponse
import forge.identity.config.AuthConfig
import forge.identity.config.Config
import forge.identity.config.DatabaseConfig
import forge.identity.config.TokenConfig
import forge.identity.db.Database
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.http.ErrorEnvelope
import forge.identity.org.OrgResponse
import forge.identity.project.ProjectResponse
import forge.identity.token.CreateTokenResponse
import forge.identity.token.ServiceAccountResponse
import forge.identity.token.TokenMetadataResponse
import forge.identity.token.TokenStore
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
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import java.time.Instant
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * API tokens + service accounts + revocation (09.05).
 * DB-backed cases require foundation Postgres `forge_identity`; skipped when unreachable.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class TokenTest {
    private val jdbcUrl = System.getenv("FORGE_IDENTITY_DB_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge_identity"

    private lateinit var db: Database
    private lateinit var tenancy: TenancyServices

    private val testAuthConfig = AuthConfig(
        sessionTtlSeconds = 3_600,
        argon2MemoryKb = 8_192,
        argon2Iterations = 2,
        loginMaxFails = 5,
    )

    private val testTokenConfig = TokenConfig(
        defaultTtlSeconds = null,
        prefixLen = 8,
    )

    private fun dbConfig() = DatabaseConfig(
        url = jdbcUrl,
        user = System.getenv("FORGE_IDENTITY_DB_USER") ?: "forge",
        password = System.getenv("FORGE_IDENTITY_DB_PASSWORD") ?: "forge",
        poolMax = 2,
        migrateOnStart = true,
        connectRetryInitialMs = 200,
        connectRetryMaxMs = 1000,
    )

    private fun cfg(token: TokenConfig = testTokenConfig) = Config(
        port = 4002,
        serviceName = "forge-identity",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
        shutdownGraceSeconds = 10,
        database = dbConfig(),
        auth = testAuthConfig,
        token = token,
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

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(Json { ignoreUnknownKeys = true })
        }
    }

    private fun withApp(
        tokenCfg: TokenConfig = testTokenConfig,
        block: suspend ApplicationTestBuilder.() -> Unit,
    ) {
        assumeTrue(::db.isInitialized)
        val probe = DbProbe { db.check() }
        val tokens = TokenServices.from(db, tenancy, tokenCfg)
        val auth = AuthServices.from(
            db = db,
            tenancy = tenancy,
            authConfig = testAuthConfig,
            tokenConfig = tokenCfg,
            tokenIntrospector = tokens.introspector,
        )
        testApplication {
            application {
                forgeIdentityModule(
                    cfg = cfg(tokenCfg),
                    readiness = Readiness(initial = true),
                    dbProbe = probe,
                    tenancy = tenancy,
                    auth = auth,
                    tokens = tokens,
                )
            }
            block()
        }
    }

    private suspend fun ApplicationTestBuilder.seedMemberProject(
        role: String = "developer",
    ): Triple<String, String, String> {
        val client = jsonClient()
        val email = "tok-${UUID.randomUUID()}@example.com"
        val user = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","display_name":"Tok"}""")
        }.body<UserResponse>()
        val org = client.post("/v1/orgs") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"Org-${UUID.randomUUID()}"}""")
        }.body<OrgResponse>()
        val projectId = "prj-${UUID.randomUUID()}"
        client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"id":"$projectId","org_id":"${org.id}","name":"P"}""")
        }.body<ProjectResponse>()
        client.post("/v1/projects/$projectId/members") {
            contentType(ContentType.Application.Json)
            setBody("""{"user_id":"${user.id}","role":"$role"}""")
        }
        return Triple(user.id, projectId, role)
    }

    @Test
    fun plaintextOnceHashAtRestListNeverReturnsSecret() = withApp {
        val client = jsonClient()
        val (userId, projectId, role) = seedMemberProject()
        val created = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"user","id":"$userId"},"project_id":"$projectId","role":"$role"}
                """.trimIndent(),
            )
        }
        assertEquals(HttpStatusCode.Created, created.status)
        val body = created.body<CreateTokenResponse>()
        assertTrue(body.token.startsWith("forge_pat_"))
        assertEquals(8, body.prefix.length)
        assertTrue(body.token.startsWith(body.prefix))
        assertNotNull(body.token_id)

        val stores = TokenServices.from(db, tenancy, testTokenConfig)
        val row = stores.tokens.findById(body.token_id)
        assertNotNull(row)
        assertEquals(TokenStore.hashToken(body.token), row!!.tokenHash)
        assertNotEquals(body.token, row.tokenHash)
        assertEquals(body.prefix, row.prefix)

        val listed = client.get("/v1/tokens?owner=$userId")
        assertEquals(HttpStatusCode.OK, listed.status)
        val meta = listed.body<List<TokenMetadataResponse>>()
        assertTrue(meta.any { it.token_id == body.token_id })
        val match = meta.first { it.token_id == body.token_id }
        assertEquals(body.prefix, match.prefix)
        assertEquals(projectId, match.project_id)
        assertEquals(role, match.role)

        val text = client.get("/v1/tokens?owner=$userId").body<String>()
        assertFalse(text.contains(body.token))
        assertFalse(text.contains("\"token\":"))
        assertTrue(text.contains("\"prefix\""))
    }

    @Test
    fun introspectUserAndServiceAccountTokens() = withApp {
        val client = jsonClient()
        val (userId, projectId, _) = seedMemberProject("developer")

        val userTok = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"user","id":"$userId"},"project_id":"$projectId","role":"developer"}
                """.trimIndent(),
            )
        }.body<CreateTokenResponse>()

        val userIntro = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${userTok.token}"}""")
        }.body<IntrospectResponse>()
        assertTrue(userIntro.active)
        assertEquals("user", userIntro.principal_type)
        assertEquals(userId, userIntro.principal_id)
        assertEquals(userId, userIntro.user_id)
        assertEquals(projectId, userIntro.project_id)
        assertEquals("developer", userIntro.role)

        val sa = client.post("/v1/service-accounts") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"project_id":"$projectId","name":"ci-${UUID.randomUUID()}","role":"service-account"}
                """.trimIndent(),
            )
        }
        assertEquals(HttpStatusCode.Created, sa.status)
        val account = sa.body<ServiceAccountResponse>()

        val sat = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"service_account","id":"${account.id}"},
                 "project_id":"$projectId","role":"service-account"}
                """.trimIndent(),
            )
        }.body<CreateTokenResponse>()
        assertTrue(sat.token.startsWith("forge_sat_"))

        val saIntro = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${sat.token}"}""")
        }.body<IntrospectResponse>()
        assertTrue(saIntro.active)
        assertEquals("service_account", saIntro.principal_type)
        assertEquals(account.id, saIntro.principal_id)
        assertEquals(projectId, saIntro.project_id)
        assertEquals("service-account", saIntro.role)
        assertEquals(null, saIntro.user_id)
    }

    @Test
    fun revokeAndExpiryMakeTokenInactive() = withApp {
        val client = jsonClient()
        val (userId, projectId, _) = seedMemberProject()

        val created = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"user","id":"$userId"},"project_id":"$projectId",
                 "role":"developer","expires_in_s":3600}
                """.trimIndent(),
            )
        }.body<CreateTokenResponse>()
        assertNotNull(created.expires_at)

        val active = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${created.token}"}""")
        }.body<IntrospectResponse>()
        assertTrue(active.active)

        val revoke = client.post("/v1/tokens/${created.token_id}/revoke")
        assertEquals(HttpStatusCode.NoContent, revoke.status)

        val inactive = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${created.token}"}""")
        }.body<IntrospectResponse>()
        assertFalse(inactive.active)

        // Expired token via store (past expires_at)
        val stores = TokenServices.from(db, tenancy, testTokenConfig)
        val expired = stores.tokens.create(
            ownerType = "user",
            ownerId = userId,
            projectId = projectId,
            role = "developer",
            expiresInSeconds = 1,
            now = Instant.now().minusSeconds(120),
        )
        assertFalse(expired.token.isActive())
        assertFalse(stores.introspector.introspect(expired.plaintext)!!.active)
    }

    @Test
    fun createForNonMemberRejectedAndDuplicateServiceAccountConflicts() = withApp {
        val client = jsonClient()
        val email = "nm-${UUID.randomUUID()}@example.com"
        val outsider = client.post("/v1/users") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","display_name":"Out"}""")
        }.body<UserResponse>()
        val (_, projectId, _) = seedMemberProject()

        val denied = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"user","id":"${outsider.id}"},"project_id":"$projectId","role":"developer"}
                """.trimIndent(),
            )
        }
        assertEquals(HttpStatusCode.Forbidden, denied.status)
        assertEquals("forbidden", denied.body<ErrorEnvelope>().error.code)

        val name = "dup-${UUID.randomUUID()}"
        val first = client.post("/v1/service-accounts") {
            contentType(ContentType.Application.Json)
            setBody("""{"project_id":"$projectId","name":"$name","role":"service-account"}""")
        }
        assertEquals(HttpStatusCode.Created, first.status)
        val second = client.post("/v1/service-accounts") {
            contentType(ContentType.Application.Json)
            setBody("""{"project_id":"$projectId","name":"$name","role":"service-account"}""")
        }
        assertEquals(HttpStatusCode.Conflict, second.status)
    }

    @Test
    fun serviceAccountLifecycleCreateTokenIntrospectRevoke() = withApp {
        val client = jsonClient()
        val (_, projectId, _) = seedMemberProject()
        val sa = client.post("/v1/service-accounts") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"project_id":"$projectId","name":"bot-${UUID.randomUUID()}","role":"service-account"}
                """.trimIndent(),
            )
        }.body<ServiceAccountResponse>()

        val tok = client.post("/v1/tokens") {
            contentType(ContentType.Application.Json)
            setBody(
                """
                {"owner":{"type":"service_account","id":"${sa.id}"},
                 "project_id":"$projectId","role":"service-account"}
                """.trimIndent(),
            )
        }.body<CreateTokenResponse>()

        val active = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${tok.token}"}""")
        }.body<IntrospectResponse>()
        assertTrue(active.active)
        assertEquals("service_account", active.principal_type)
        assertEquals("service-account", active.role)

        assertEquals(
            HttpStatusCode.NoContent,
            client.post("/v1/tokens/${tok.token_id}/revoke").status,
        )
        val after = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${tok.token}"}""")
        }.body<IntrospectResponse>()
        assertFalse(after.active)

        val list = client.get("/v1/tokens?owner=${sa.id}&owner_type=service_account")
        assertEquals(HttpStatusCode.OK, list.status)
        val arr = Json.parseToJsonElement(list.body<String>()).jsonArray
        assertTrue(arr.isNotEmpty())
        assertFalse(arr[0].jsonObject.containsKey("token"))
    }
}
