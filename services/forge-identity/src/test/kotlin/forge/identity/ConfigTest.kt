package forge.identity

import forge.identity.config.loadConfig
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ConfigTest {
    @Test
    fun parsesValidEnv() {
        val cfg = loadConfig(
            mapOf(
                "PORT" to "4002",
                "FORGE_SERVICE_NAME" to "forge-identity",
                "FORGE_SERVICE_VERSION" to "0.1.0",
                "FORGE_LOG_LEVEL" to "debug",
                "FORGE_ENV" to "test",
                "FORGE_SHUTDOWN_GRACE_SECONDS" to "15",
                "FORGE_IDENTITY_DB_URL" to "jdbc:postgresql://postgres:5432/forge_identity",
                "FORGE_IDENTITY_DB_USER" to "identity",
                "FORGE_IDENTITY_DB_PASSWORD" to "secret",
                "FORGE_IDENTITY_DB_POOL_MAX" to "5",
            ),
        )
        assertEquals(4002, cfg.port)
        assertEquals("forge-identity", cfg.serviceName)
        assertEquals("0.1.0", cfg.serviceVersion)
        assertEquals("debug", cfg.logLevel)
        assertEquals("test", cfg.env)
        assertEquals(15, cfg.shutdownGraceSeconds)
        assertEquals("jdbc:postgresql://postgres:5432/forge_identity", cfg.database.url)
        assertEquals("identity", cfg.database.user)
        assertEquals("secret", cfg.database.password)
        assertEquals(5, cfg.database.poolMax)
        assertEquals(true, cfg.database.migrateOnStart)
    }

    @Test
    fun defaultsWhenOptionalMissing() {
        val cfg = loadConfig(
            mapOf(
                "FORGE_IDENTITY_DB_URL" to "jdbc:postgresql://127.0.0.1:5001/forge_identity",
            ),
        )
        assertEquals(4002, cfg.port)
        assertEquals("forge-identity", cfg.serviceName)
        assertEquals("0.1.0", cfg.serviceVersion)
        assertEquals("info", cfg.logLevel)
        assertEquals("development", cfg.env)
        assertEquals(10, cfg.shutdownGraceSeconds)
        assertEquals("forge", cfg.database.user)
        assertEquals("forge", cfg.database.password)
        assertEquals(10, cfg.database.poolMax)
        assertEquals(true, cfg.database.migrateOnStart)
    }

    @Test
    fun missingDbUrlFailsFast() {
        val error = assertFailsWith<IllegalArgumentException> {
            loadConfig(mapOf("PORT" to "4002"))
        }
        assertTrue(error.message!!.contains("FORGE_IDENTITY_DB_URL"))
    }

    @Test
    fun invalidPortRejected() {
        assertFailsWith<IllegalArgumentException> {
            loadConfig(
                mapOf(
                    "PORT" to "not-a-port",
                    "FORGE_IDENTITY_DB_URL" to "jdbc:postgresql://127.0.0.1:5001/forge_identity",
                ),
            )
        }
    }
}
