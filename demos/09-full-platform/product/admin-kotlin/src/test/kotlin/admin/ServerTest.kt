package admin

import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.client.request.put
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ServerTest {
    private val cfg = Config(
        port = 8080,
        serviceName = "incident-admin",
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
        assertEquals("incident-admin", body.service)
        assertEquals("kotlin", body.language)
        assertEquals("running", body.status)
        assertTrue(body.uptimeSeconds != null && body.uptimeSeconds!! > 0.0)
    }

    @Test
    fun adminConfigRoundTrip() = testApplication {
        val store = AtomicReference(AdminConfig())
        application {
            configureContractRoutes(cfg, adminConfig = store)
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val updated = AdminConfig(notifyEnabled = false, defaultSeverity = "high", retentionDays = 7)
        val putResp = client.put("/admin/config") {
            contentType(ContentType.Application.Json)
            setBody(updated)
        }
        assertEquals(HttpStatusCode.OK, putResp.status)
        val getResp = client.get("/admin/config")
        assertEquals(updated, getResp.body<AdminConfig>())
    }

    @Test
    fun loadConfigRequiresPort() {
        assertFailsWith<IllegalArgumentException> {
            loadConfig(mapOf("FORGE_LOG_LEVEL" to "info"))
        }
    }

    @Test
    fun loadConfigDefaults() {
        val loaded = loadConfig(mapOf("PORT" to "8080"))
        assertEquals(8080, loaded.port)
        assertEquals("incident-admin", loaded.serviceName)
    }
}
