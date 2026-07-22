package demo

import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import java.util.concurrent.atomic.AtomicLong
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ServerTest {
    private val cfg = Config(
        port = 8080,
        serviceName = "demo-kotlin-api",
        serviceVersion = "0.1.0",
        logLevel = "info",
        env = "test",
    )

    @Test
    fun liveAndReady() = testApplication {
        application {
            configureContractRoutes(cfg)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        for (path in listOf("/health/live", "/health/ready")) {
            val response = client.get(path)
            assertEquals(HttpStatusCode.OK, response.status)
            val body = response.body<HealthResponse>()
            assertEquals("ok", body.status)
        }
    }

    @Test
    fun identity() = testApplication {
        val startedAt = AtomicLong(System.currentTimeMillis() - 2_000)
        application {
            configureContractRoutes(cfg, startedAt)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/")
        assertEquals(HttpStatusCode.OK, response.status)
        val body = response.body<IdentityResponse>()
        assertEquals("demo-kotlin-api", body.service)
        assertEquals("kotlin", body.language)
        assertEquals("running", body.status)
        assertEquals("0.1.0", body.version)
        assertTrue(body.uptimeSeconds != null && body.uptimeSeconds!! > 0.0)
    }

    @Test
    fun loadConfigRequiresPort() {
        assertFailsWith<IllegalArgumentException> {
            loadConfig(mapOf("FORGE_LOG_LEVEL" to "info"))
        }
    }

    @Test
    fun loadConfigRejectsInvalidPort() {
        assertFailsWith<IllegalArgumentException> {
            loadConfig(mapOf("PORT" to "not-a-port", "FORGE_LOG_LEVEL" to "info"))
        }
    }

    @Test
    fun loadConfigRejectsInvalidLogLevel() {
        assertFailsWith<IllegalArgumentException> {
            loadConfig(mapOf("PORT" to "8080", "FORGE_LOG_LEVEL" to "verbose"))
        }
    }

    @Test
    fun loadConfigDefaults() {
        val loaded = loadConfig(mapOf("PORT" to "8080"))
        assertEquals(8080, loaded.port)
        assertEquals("demo-kotlin-api", loaded.serviceName)
        assertEquals("info", loaded.logLevel)
    }
}
