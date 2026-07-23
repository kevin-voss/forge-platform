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
                "FORGE_OTEL_ENABLED" to "false",
                "OTEL_EXPORTER_OTLP_ENDPOINT" to "http://collector:4317",
                "FORGE_ENV" to "test",
                "FORGE_AUTH_MODE" to "dev",
                "FORGE_SHUTDOWN_GRACE_SECONDS" to "15",
            ),
        )
        assertEquals(8080, cfg.port)
        assertEquals("forge-control", cfg.serviceName)
        assertEquals("0.1.0", cfg.serviceVersion)
        assertEquals("debug", cfg.logLevel)
        assertEquals(false, cfg.otelEnabled)
        assertEquals("http://collector:4317", cfg.otlpEndpoint)
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
        assertEquals(true, cfg.otelEnabled)
        assertEquals("http://otel-collector:4317", cfg.otlpEndpoint)
        assertEquals("development", cfg.env)
        assertEquals("enforce", cfg.authMode)
        assertEquals("http://forge-identity:4002", cfg.identityUrl)
        assertEquals(10L, cfg.introspectCacheTtlS)
        assertEquals(10L, cfg.authzCacheTtlS)
        assertEquals(10, cfg.shutdownGraceSeconds)
        assertEquals("jdbc:postgresql://127.0.0.1:5001/forge", cfg.database.url)
        assertEquals("forge", cfg.database.user)
        assertEquals("forge", cfg.database.password)
        assertEquals("control", cfg.database.schema)
        assertEquals(10, cfg.database.poolMax)
        assertEquals(true, cfg.database.migrateOnStart)
        assertEquals(true, cfg.reconcileEnabled)
        assertEquals(2_000L, cfg.reconcileIntervalMs)
        assertEquals("http://forge-runtime:4102", cfg.runtimeUrl)
        assertEquals(true, cfg.historyEnabled)
        assertEquals(true, cfg.startupAdoptLabels)
        assertEquals(true, cfg.schedulerEnabled)
        assertEquals("least-allocated", cfg.schedulerStrategy)
        assertEquals("node-local", cfg.schedulerLocalNodeId)
        assertEquals("fake", cfg.dbProvisioner)
        assertEquals("forge-net", cfg.dbManagedNetwork)
        assertEquals("postgres:16", cfg.dbPostgresImage)
        assertEquals("127.0.0.1", cfg.dbEndpointHost)
    }

    @Test
    fun parsesReconcileEnv() {
        val cfg = loadAppConfig(
            mapOf(
                "PORT" to "8080",
                "FORGE_RECONCILE_ENABLED" to "false",
                "FORGE_RECONCILE_INTERVAL_MS" to "500",
                "FORGE_RECONCILE_MAX_ACTIONS_PER_TICK" to "3",
                "FORGE_RUNTIME_URL" to "http://127.0.0.1:4102",
                "FORGE_GATEWAY_URL" to "http://127.0.0.1:4000",
                "FORGE_ROLLOUT_BATCH_SIZE" to "2",
                "FORGE_ROLLOUT_TIMEOUT_S" to "90",
                "FORGE_ROLLBACK_ENABLED" to "false",
                "FORGE_READINESS_POLL_MS" to "250",
                "FORGE_READINESS_MAX_WAIT_S" to "30",
                "FORGE_HISTORY_ENABLED" to "false",
                "FORGE_STARTUP_ADOPT_LABELS" to "false",
                "FORGE_SCHEDULER_ENABLED" to "false",
                "FORGE_SCHEDULER_STRATEGY" to "first-fit",
                "FORGE_SCHEDULER_LOCAL_NODE_ID" to "node-a",
            ),
        )
        assertEquals(false, cfg.reconcileEnabled)
        assertEquals(500L, cfg.reconcileIntervalMs)
        assertEquals(3, cfg.reconcileMaxActionsPerTick)
        assertEquals("http://127.0.0.1:4102", cfg.runtimeUrl)
        assertEquals("http://127.0.0.1:4000", cfg.gatewayUrl)
        assertEquals(2, cfg.rolloutBatchSizeOverride)
        assertEquals(90, cfg.rolloutTimeoutOverride)
        assertEquals(false, cfg.rollbackEnabled)
        assertEquals(250L, cfg.readinessPollMs)
        assertEquals(30L, cfg.readinessMaxWaitSeconds)
        assertEquals(false, cfg.historyEnabled)
        assertEquals(false, cfg.startupAdoptLabels)
        assertEquals(false, cfg.schedulerEnabled)
        assertEquals("first-fit", cfg.schedulerStrategy)
        assertEquals("node-a", cfg.schedulerLocalNodeId)
    }

    @Test
    fun parsesDatabaseEnv() {
        val cfg = loadAppConfig(
            mapOf(
                "PORT" to "8080",
                "DATABASE_URL" to "jdbc:postgresql://postgres:5432/forge",
                "DATABASE_USER" to "control",
                "DATABASE_PASSWORD" to "secret",
                "DATABASE_SCHEMA" to "control",
                "DATABASE_POOL_MAX" to "5",
                "DATABASE_MIGRATE_ON_START" to "false",
            ),
        )
        assertEquals("jdbc:postgresql://postgres:5432/forge", cfg.database.url)
        assertEquals("control", cfg.database.user)
        assertEquals("secret", cfg.database.password)
        assertEquals("control", cfg.database.schema)
        assertEquals(5, cfg.database.poolMax)
        assertEquals(false, cfg.database.migrateOnStart)
    }

    @Test
    fun rejectsInvalidDatabaseSchema() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "DATABASE_SCHEMA" to "control;drop"))
        }
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
    fun rejectsInvalidOtelEnabled() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_OTEL_ENABLED" to "sometimes"))
        }
    }

    @Test
    fun rejectsInvalidShutdownGrace() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_SHUTDOWN_GRACE_SECONDS" to "-1"))
        }
    }

    @Test
    fun rejectsInvalidReconcileEnabled() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_RECONCILE_ENABLED" to "maybe"))
        }
    }

    @Test
    fun rejectsInvalidReconcileInterval() {
        assertFailsWith<IllegalArgumentException> {
            loadAppConfig(mapOf("PORT" to "8080", "FORGE_RECONCILE_INTERVAL_MS" to "0"))
        }
    }
}
