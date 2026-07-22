package forge.control

import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.http.AlwaysHealthyDb
import forge.control.http.DbProbe
import forge.control.http.HealthResponse
import forge.control.http.Readiness
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
    private val cfg = AppConfig(
        port = 8080,
        serviceName = "forge-control",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
        authMode = "dev",
        shutdownGraceSeconds = 10,
        database = DatabaseConfig(
            url = "jdbc:postgresql://127.0.0.1:5001/forge",
            user = "forge",
            password = "forge",
            schema = "control",
            poolMax = 10,
            migrateOnStart = true,
        ),
    )

    @Test
    fun liveReturnsLiveStatus() = testApplication {
        application {
            forgeControlModule(cfg, Readiness(initial = true), AlwaysHealthyDb)
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
            forgeControlModule(cfg, Readiness(initial = true), AlwaysHealthyDb)
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
            forgeControlModule(cfg, Readiness(initial = false), AlwaysHealthyDb)
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
            forgeControlModule(cfg, Readiness(initial = true), failingDb)
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
}
