package forge.control

import forge.control.config.loadAppConfig
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class AppConfigTest {
    @Test
    fun parsesValidEnv() {
        val cfg = loadAppConfig(
            mapOf(
                "PORT" to "8080",
                "FORGE_SERVICE_NAME" to "forge-control",
                "FORGE_SERVICE_VERSION" to "0.1.0",
                "FORGE_LOG_LEVEL" to "debug",
                "FORGE_ENV" to "test",
                "FORGE_AUTH_MODE" to "dev",
                "FORGE_SHUTDOWN_GRACE_SECONDS" to "15",
            ),
        )
        assertEquals(8080, cfg.port)
        assertEquals("forge-control", cfg.serviceName)
        assertEquals("0.1.0", cfg.serviceVersion)
        assertEquals("debug", cfg.logLevel)
        assertEquals("test", cfg.env)
        assertEquals("dev", cfg.authMode)
        assertEquals(15, cfg.shutdownGraceSeconds)
    }

    @Test
    fun defaultsWhenOptionalMissing() {
        val cfg = loadAppConfig(mapOf("PORT" to "4001"))
        assertEquals(4001, cfg.port)
        assertEquals("forge-control", cfg.serviceName)
        assertEquals("0.1.0", cfg.serviceVersion)
        assertEquals("info", cfg.logLevel)
        assertEquals("development", cfg.env)
        assertEquals("dev", cfg.authMode)
        assertEquals(10, cfg.shutdownGraceSeconds)
    }

    @Test
    fun portWinsOverForgeHttpPort() {
        val cfg = loadAppConfig(
            mapOf(
                "PORT" to "8080",
                "FORGE_HTTP_PORT" to "9090",
            ),
        )
        assertEquals(8080, cfg.port)
    }

    @Test
    fun fallsBackToForgeHttpPort() {
        val cfg = loadAppConfig(mapOf("FORGE_HTTP_PORT" to "9090"))
        assertEquals(9090, cfg.port)
    }

    @Test
    fun rejectsMissingPort() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("FORGE_LOG_LEVEL" to "info"))
        }
    }

    @Test
    fun rejectsNonIntegerPort() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "not-a-port"))
        }
    }

    @Test
    fun rejectsOutOfRangePort() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "70000"))
        }
    }

    @Test
    fun rejectsZeroPort() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "0"))
        }
    }

    @Test
    fun rejectsInvalidLogLevel() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_LOG_LEVEL" to "verbose"))
        }
    }

    @Test
    fun rejectsInvalidShutdownGrace() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_SHUTDOWN_GRACE_SECONDS" to "-1"))
        }
    }
}
