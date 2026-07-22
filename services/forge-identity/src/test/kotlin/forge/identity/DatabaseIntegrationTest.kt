package forge.identity

import forge.identity.config.DatabaseConfig
import forge.identity.db.Database
import forge.identity.health.DbProbe
import forge.identity.health.HealthResponse
import forge.identity.health.Readiness
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import forge.identity.config.Config

/**
 * Requires foundation Postgres with the `forge_identity` database
 * (`jdbc:postgresql://127.0.0.1:5001/forge_identity`). Skipped when unreachable.
 */
class DatabaseIntegrationTest {
    private val jdbcUrl = System.getenv("FORGE_IDENTITY_DB_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge_identity"

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

    private fun assumePostgres() {
        try {
            Database.open(dbConfig()).close()
        } catch (_: Exception) {
            assumeTrue(false, "Postgres forge_identity unreachable at $jdbcUrl")
        }
    }

    @Test
    fun migratesAndReadyReturns200() {
        assumePostgres()
        Database.open(dbConfig()).use { db ->
            val result = db.migrate()
            assertNull(db.check())
            assert(result.migrationsExecuted >= 0)

            val probe = DbProbe { db.check() }
            testApplication {
                application {
                    forgeIdentityModule(cfg(), Readiness(initial = true), probe)
                }
                val client = createClient {
                    install(ContentNegotiation) {
                        json(Json { ignoreUnknownKeys = true })
                    }
                }
                val ready = client.get("/health/ready")
                assertEquals(HttpStatusCode.OK, ready.status)
                assertEquals("ready", ready.body<HealthResponse>().status)
                val live = client.get("/health/live")
                assertEquals(HttpStatusCode.OK, live.status)
                assertEquals("live", live.body<HealthResponse>().status)
            }
        }
    }

    @Test
    fun dbDownKeepsLiveWhileReadyFails() {
        val failing = DbProbe { "connection refused" }
        testApplication {
            application {
                forgeIdentityModule(cfg(), Readiness(initial = true), failing)
            }
            val client = createClient {
                install(ContentNegotiation) {
                    json(Json { ignoreUnknownKeys = true })
                }
            }
            val live = client.get("/health/live")
            assertEquals(HttpStatusCode.OK, live.status)
            assertEquals("live", live.body<HealthResponse>().status)
            val ready = client.get("/health/ready")
            assertEquals(HttpStatusCode.ServiceUnavailable, ready.status)
            assertEquals("not_ready", ready.body<HealthResponse>().status)
        }
    }
}
