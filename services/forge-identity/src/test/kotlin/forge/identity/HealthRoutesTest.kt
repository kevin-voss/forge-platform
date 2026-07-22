package forge.identity

import forge.identity.config.Config
import forge.identity.config.DatabaseConfig
import forge.identity.health.AlwaysHealthyDb
import forge.identity.health.DbProbe
import forge.identity.health.HealthResponse
import forge.identity.health.IdentityResponse
import forge.identity.health.Readiness
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals

class HealthRoutesTest {
    private val cfg = Config(
        port = 4002,
        serviceName = "forge-identity",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
        shutdownGraceSeconds = 10,
        database = DatabaseConfig(
            url = "jdbc:postgresql://127.0.0.1:5001/forge_identity",
            user = "forge",
            password = "forge",
            poolMax = 10,
            migrateOnStart = true,
            connectRetryInitialMs = 500,
            connectRetryMaxMs = 5000,
        ),
    )

    @Test
    fun liveReturnsLiveStatus() = testApplication {
        application {
            forgeIdentityModule(cfg, Readiness(initial = true), AlwaysHealthyDb)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/health/live")
        assertEquals(HttpStatusCode.OK, response.status)
        assertEquals("live", response.body<HealthResponse>().status)
    }

    @Test
    fun readyReturnsReadyWhenStartedAndDbHealthy() = testApplication {
        application {
            forgeIdentityModule(cfg, Readiness(initial = true), AlwaysHealthyDb)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/health/ready")
        assertEquals(HttpStatusCode.OK, response.status)
        assertEquals("ready", response.body<HealthResponse>().status)
    }

    @Test
    fun readyReturns503BeforeStarted() = testApplication {
        application {
            forgeIdentityModule(cfg, Readiness(initial = false), AlwaysHealthyDb)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/health/ready")
        assertEquals(HttpStatusCode.ServiceUnavailable, response.status)
        assertEquals("not_ready", response.body<HealthResponse>().status)
    }

    @Test
    fun readyReturns503WhenDbUnavailable() = testApplication {
        val failingDb = DbProbe { "connection refused" }
        application {
            forgeIdentityModule(cfg, Readiness(initial = true), failingDb)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/health/ready")
        assertEquals(HttpStatusCode.ServiceUnavailable, response.status)
        assertEquals("not_ready", response.body<HealthResponse>().status)
    }

    @Test
    fun rootReturnsIdentityShape() = testApplication {
        application {
            forgeIdentityModule(cfg, Readiness(initial = true), AlwaysHealthyDb)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/")
        assertEquals(HttpStatusCode.OK, response.status)
        val body = response.body<IdentityResponse>()
        assertEquals("forge-identity", body.service)
        assertEquals("kotlin", body.language)
        assertEquals("running", body.status)
    }
}
