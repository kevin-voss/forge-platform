package forge.identity

import forge.identity.auth.IntrospectResponse
import forge.identity.auth.LoginResponse
import forge.identity.auth.PasswordHasher
import forge.identity.auth.RegisterResponse
import forge.identity.auth.SessionStore
import forge.identity.config.AuthConfig
import forge.identity.config.Config
import forge.identity.config.DatabaseConfig
import forge.identity.db.Database
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.http.ErrorEnvelope
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
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
 * Auth registration / login / sessions (09.03).
 * DB-backed cases require foundation Postgres `forge_identity`; skipped when unreachable.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class AuthTest {
    private val jdbcUrl = System.getenv("FORGE_IDENTITY_DB_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge_identity"

    private lateinit var db: Database
    private lateinit var tenancy: TenancyServices

    private val testAuthConfig = AuthConfig(
        sessionTtlSeconds = 3_600,
        argon2MemoryKb = 8_192,
        argon2Iterations = 2,
        loginMaxFails = 3,
        loginLockoutWindowSeconds = 900,
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

    private fun cfg(auth: AuthConfig = testAuthConfig) = Config(
        port = 4002,
        serviceName = "forge-identity",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
        shutdownGraceSeconds = 10,
        database = dbConfig(),
        auth = auth,
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

    private fun uniqueEmail(prefix: String = "auth"): String =
        "$prefix-${UUID.randomUUID()}@example.com"

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(Json { ignoreUnknownKeys = true })
        }
    }

    private fun withApp(
        authCfg: AuthConfig = testAuthConfig,
        block: suspend ApplicationTestBuilder.() -> Unit,
    ) {
        val probe = DbProbe { db.check() }
        val auth = AuthServices.from(db, tenancy, authCfg)
        testApplication {
            application {
                forgeIdentityModule(
                    cfg = cfg(authCfg),
                    readiness = Readiness(initial = true),
                    dbProbe = probe,
                    tenancy = tenancy,
                    auth = auth,
                )
            }
            block()
        }
    }

    @Test
    fun passwordHasherAcceptsCorrectRejectsWrongAndSaltsDiffer() {
        val hasher = PasswordHasher(memoryKb = 8_192, iterations = 2)
        val a = hasher.hash("s3cret!!")
        val b = hasher.hash("s3cret!!")
        assertTrue(a.startsWith("\$argon2id\$"))
        assertNotEquals(a, b, "each hash must use a unique salt")
        assertTrue(hasher.verify(a, "s3cret!!"))
        assertFalse(hasher.verify(a, "wrong-password"))
        assertFalse(hasher.verify(b, "nope"))
    }

    @Test
    fun introspectInactiveForExpiredRevokedUnknown() {
        assumeTrue(::db.isInitialized)
        val shortTtl = AuthConfig(
            sessionTtlSeconds = 1,
            argon2MemoryKb = 8_192,
            argon2Iterations = 2,
            loginMaxFails = 5,
        )
        val auth = AuthServices.from(db, tenancy, shortTtl)
        val email = uniqueEmail("exp")
        val userId = auth.auth.register(email, "s3cret!!", "Exp")
        val created = auth.sessions.create(userId, now = Instant.now().minusSeconds(120))
        assertFalse(created.session.isActive())
        assertFalse(auth.auth.introspect(created.token).active)

        val live = auth.sessions.create(userId)
        assertTrue(auth.auth.introspect(live.token).active)
        auth.sessions.revokeByToken(live.token)
        assertFalse(auth.auth.introspect(live.token).active)

        assertFalse(auth.auth.introspect("totally-unknown-token").active)
    }

    @Test
    fun lockoutTriggersAfterMaxFails() = withApp {
        val client = jsonClient()
        val email = uniqueEmail("lock")
        val reg = client.post("/v1/auth/register") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"s3cret!!","display_name":"Lock"}""")
        }
        assertEquals(HttpStatusCode.Created, reg.status)

        repeat(3) {
            val fail = client.post("/v1/auth/login") {
                contentType(ContentType.Application.Json)
                setBody("""{"email":"$email","password":"wrong-pass"}""")
            }
            assertEquals(HttpStatusCode.Unauthorized, fail.status)
        }

        val locked = client.post("/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"s3cret!!"}""")
        }
        assertEquals(HttpStatusCode.TooManyRequests, locked.status)
        val err = locked.body<ErrorEnvelope>()
        assertEquals("rate_limited", err.error.code)
    }

    @Test
    fun registerLoginIntrospectLogoutLifecycle() = withApp {
        val client = jsonClient()
        val email = uniqueEmail("life")
        val reg = client.post("/v1/auth/register") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"s3cret!!","display_name":"Life"}""")
        }
        assertEquals(HttpStatusCode.Created, reg.status)
        val userId = reg.body<RegisterResponse>().user_id
        assertTrue(userId.isNotBlank())

        // Credential stored as Argon2id, not plaintext
        val stores = AuthServices.from(db, tenancy, testAuthConfig)
        val cred = stores.credentials.findByUserId(userId)
        assertNotNull(cred)
        assertTrue(cred!!.hash.startsWith("\$argon2id\$"))
        assertFalse(cred.hash.contains("s3cret"))

        val login = client.post("/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"s3cret!!"}""")
        }
        assertEquals(HttpStatusCode.OK, login.status)
        val session = login.body<LoginResponse>()
        assertTrue(session.session_token.isNotBlank())
        assertTrue(session.expires_at.isNotBlank())

        // Only hash at rest
        val stored = SessionStore.hashToken(session.session_token)
        val row = stores.sessions.findByToken(session.session_token)
        assertNotNull(row)
        assertEquals(stored, row!!.tokenHash)
        assertNotEquals(session.session_token, row.tokenHash)

        val active = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${session.session_token}"}""")
        }
        assertEquals(HttpStatusCode.OK, active.status)
        val introspect = active.body<IntrospectResponse>()
        assertTrue(introspect.active)
        assertEquals("user", introspect.principal_type)
        assertEquals(userId, introspect.user_id)
        assertNotNull(introspect.memberships)

        val logout = client.post("/v1/auth/logout") {
            header("Authorization", "Bearer ${session.session_token}")
        }
        assertEquals(HttpStatusCode.NoContent, logout.status)

        val inactive = client.post("/v1/auth/introspect") {
            contentType(ContentType.Application.Json)
            setBody("""{"token":"${session.session_token}"}""")
        }
        assertEquals(HttpStatusCode.OK, inactive.status)
        assertFalse(inactive.body<IntrospectResponse>().active)
    }

    @Test
    fun wrongPasswordUniform401AndUnknownUser() = withApp {
        val client = jsonClient()
        val email = uniqueEmail("uni")
        client.post("/v1/auth/register") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"s3cret!!","display_name":"Uni"}""")
        }

        val wrong = client.post("/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"$email","password":"bad-password"}""")
        }
        assertEquals(HttpStatusCode.Unauthorized, wrong.status)
        val wrongErr = wrong.body<ErrorEnvelope>()
        assertEquals("unauthorized", wrongErr.error.code)
        assertEquals("invalid credentials", wrongErr.error.message)

        val missing = client.post("/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody("""{"email":"missing-${UUID.randomUUID()}@example.com","password":"s3cret!!"}""")
        }
        assertEquals(HttpStatusCode.Unauthorized, missing.status)
        val missingErr = missing.body<ErrorEnvelope>()
        assertEquals("unauthorized", missingErr.error.code)
        assertEquals("invalid credentials", missingErr.error.message)
    }

    @Test
    fun sessionSurvivesStoreRecreationAndRevocationPersists() {
        assumeTrue(::db.isInitialized)
        val email = uniqueEmail("persist")
        val first = AuthServices.from(db, tenancy, testAuthConfig)
        val userId = first.auth.register(email, "s3cret!!", "Persist")
        val created = first.sessions.create(userId)
        assertTrue(first.auth.introspect(created.token).active)

        val second = AuthServices.from(db, tenancy, testAuthConfig)
        assertTrue(second.auth.introspect(created.token).active)

        second.sessions.revokeByToken(created.token)
        val third = AuthServices.from(db, tenancy, testAuthConfig)
        assertFalse(third.auth.introspect(created.token).active)
    }
}
