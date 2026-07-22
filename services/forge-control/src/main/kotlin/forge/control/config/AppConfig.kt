package forge.control.config

data class DatabaseConfig(
    val url: String,
    val user: String,
    val password: String,
    val schema: String,
    val poolMax: Int,
    val migrateOnStart: Boolean,
)

data class AppConfig(
    val port: Int,
    val serviceName: String,
    val serviceVersion: String,
    val logLevel: String,
    val otelEnabled: Boolean,
    val otlpEndpoint: String,
    val env: String,
    val authMode: String,
    val shutdownGraceSeconds: Int,
    val database: DatabaseConfig,
    val reconcileEnabled: Boolean = true,
    val reconcileIntervalMs: Long = 2_000,
    val reconcileMaxActionsPerTick: Int = 5,
    val runtimeUrl: String = "http://forge-runtime:4102",
    val gatewayUrl: String = "http://forge-gateway:4000",
    val rolloutBatchSizeOverride: Int? = null,
    val rolloutTimeoutOverride: Int? = null,
    val rollbackEnabled: Boolean = true,
    val readinessPollMs: Long = 1_000,
    val readinessMaxWaitSeconds: Long = 60,
    val historyEnabled: Boolean = true,
    val startupAdoptLabels: Boolean = true,
    val schedulerEnabled: Boolean = true,
    val schedulerStrategy: String = "single-node",
    val schedulerLocalNodeId: String = "node-local",
)

fun loadAppConfig(env: Map<String, String> = System.getenv()): AppConfig {
    val portRaw = env["PORT"]?.trim().orEmpty().ifEmpty {
        env["FORGE_HTTP_PORT"]?.trim().orEmpty()
    }
    if (portRaw.isEmpty()) {
        throw IllegalArgumentException("PORT is required (or FORGE_HTTP_PORT as fallback)")
    }
    val port = portRaw.toIntOrNull()
        ?: throw IllegalArgumentException("PORT must be an integer 1–65535, got '$portRaw'")
    if (port !in 1..65535) {
        throw IllegalArgumentException("PORT must be an integer 1–65535, got '$portRaw'")
    }

    val level = env["FORGE_LOG_LEVEL"]?.trim()?.lowercase().orEmpty().ifEmpty { "info" }
    if (level !in setOf("debug", "info", "warn", "error")) {
        throw IllegalArgumentException("FORGE_LOG_LEVEL must be debug|info|warn|error, got '$level'")
    }

    val otelEnabledRaw = env["FORGE_OTEL_ENABLED"]?.trim()?.lowercase().orEmpty().ifEmpty { "true" }
    val otelEnabled = when (otelEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_OTEL_ENABLED must be true|false, got '$otelEnabledRaw'",
        )
    }

    val graceRaw = env["FORGE_SHUTDOWN_GRACE_SECONDS"]?.trim().orEmpty().ifEmpty { "10" }
    val grace = graceRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got '$graceRaw'",
        )
    if (grace < 0) {
        throw IllegalArgumentException(
            "FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got '$graceRaw'",
        )
    }

    val poolRaw = env["DATABASE_POOL_MAX"]?.trim().orEmpty().ifEmpty { "10" }
    val poolMax = poolRaw.toIntOrNull()
        ?: throw IllegalArgumentException("DATABASE_POOL_MAX must be a positive integer, got '$poolRaw'")
    if (poolMax < 1) {
        throw IllegalArgumentException("DATABASE_POOL_MAX must be a positive integer, got '$poolRaw'")
    }

    val migrateRaw = env["DATABASE_MIGRATE_ON_START"]?.trim()?.lowercase().orEmpty().ifEmpty { "true" }
    val migrateOnStart = when (migrateRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "DATABASE_MIGRATE_ON_START must be true|false, got '$migrateRaw'",
        )
    }

    val schema = env["DATABASE_SCHEMA"]?.trim().orEmpty().ifEmpty { "control" }
    if (!schema.matches(Regex("^[a-zA-Z_][a-zA-Z0-9_]*$"))) {
        throw IllegalArgumentException(
            "DATABASE_SCHEMA must be a simple SQL identifier, got '$schema'",
        )
    }

    val reconcileEnabledRaw = env["FORGE_RECONCILE_ENABLED"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val reconcileEnabled = when (reconcileEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_RECONCILE_ENABLED must be true|false, got '$reconcileEnabledRaw'",
        )
    }

    val intervalRaw = env["FORGE_RECONCILE_INTERVAL_MS"]?.trim().orEmpty().ifEmpty { "2000" }
    val reconcileIntervalMs = intervalRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_RECONCILE_INTERVAL_MS must be a positive integer, got '$intervalRaw'",
        )
    if (reconcileIntervalMs < 1) {
        throw IllegalArgumentException(
            "FORGE_RECONCILE_INTERVAL_MS must be a positive integer, got '$intervalRaw'",
        )
    }

    val maxActionsRaw = env["FORGE_RECONCILE_MAX_ACTIONS_PER_TICK"]?.trim().orEmpty()
        .ifEmpty { "5" }
    val reconcileMaxActionsPerTick = maxActionsRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_RECONCILE_MAX_ACTIONS_PER_TICK must be a non-negative integer, got '$maxActionsRaw'",
        )
    if (reconcileMaxActionsPerTick < 0) {
        throw IllegalArgumentException(
            "FORGE_RECONCILE_MAX_ACTIONS_PER_TICK must be a non-negative integer, got '$maxActionsRaw'",
        )
    }

    val runtimeUrl = env["FORGE_RUNTIME_URL"]?.trim().orEmpty()
        .ifEmpty { "http://forge-runtime:4102" }
    if (!runtimeUrl.startsWith("http://") && !runtimeUrl.startsWith("https://")) {
        throw IllegalArgumentException(
            "FORGE_RUNTIME_URL must be an http(s) URL, got '$runtimeUrl'",
        )
    }

    val gatewayUrl = env["FORGE_GATEWAY_URL"]?.trim().orEmpty()
        .ifEmpty { "http://forge-gateway:4000" }
    if (!gatewayUrl.startsWith("http://") && !gatewayUrl.startsWith("https://")) {
        throw IllegalArgumentException(
            "FORGE_GATEWAY_URL must be an http(s) URL, got '$gatewayUrl'",
        )
    }

    val batchOverrideRaw = env["FORGE_ROLLOUT_BATCH_SIZE"]?.trim().orEmpty()
    val rolloutBatchSizeOverride = if (batchOverrideRaw.isEmpty()) {
        null
    } else {
        val parsed = batchOverrideRaw.toIntOrNull()
            ?: throw IllegalArgumentException(
                "FORGE_ROLLOUT_BATCH_SIZE must be a positive integer, got '$batchOverrideRaw'",
            )
        if (parsed < 1) {
            throw IllegalArgumentException(
                "FORGE_ROLLOUT_BATCH_SIZE must be a positive integer, got '$batchOverrideRaw'",
            )
        }
        parsed
    }

    val timeoutOverrideRaw = env["FORGE_ROLLOUT_TIMEOUT_S"]?.trim().orEmpty()
    val rolloutTimeoutOverride = if (timeoutOverrideRaw.isEmpty()) {
        null
    } else {
        val parsed = timeoutOverrideRaw.toIntOrNull()
            ?: throw IllegalArgumentException(
                "FORGE_ROLLOUT_TIMEOUT_S must be a positive integer, got '$timeoutOverrideRaw'",
            )
        if (parsed < 1) {
            throw IllegalArgumentException(
                "FORGE_ROLLOUT_TIMEOUT_S must be a positive integer, got '$timeoutOverrideRaw'",
            )
        }
        parsed
    }

    val rollbackEnabledRaw = env["FORGE_ROLLBACK_ENABLED"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val rollbackEnabled = when (rollbackEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_ROLLBACK_ENABLED must be true|false, got '$rollbackEnabledRaw'",
        )
    }

    val readinessPollRaw = env["FORGE_READINESS_POLL_MS"]?.trim().orEmpty().ifEmpty { "1000" }
    val readinessPollMs = readinessPollRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_READINESS_POLL_MS must be a positive integer, got '$readinessPollRaw'",
        )
    if (readinessPollMs < 1) {
        throw IllegalArgumentException(
            "FORGE_READINESS_POLL_MS must be a positive integer, got '$readinessPollRaw'",
        )
    }

    val readinessMaxRaw = env["FORGE_READINESS_MAX_WAIT_S"]?.trim().orEmpty().ifEmpty { "60" }
    val readinessMaxWaitSeconds = readinessMaxRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_READINESS_MAX_WAIT_S must be a positive integer, got '$readinessMaxRaw'",
        )
    if (readinessMaxWaitSeconds < 1) {
        throw IllegalArgumentException(
            "FORGE_READINESS_MAX_WAIT_S must be a positive integer, got '$readinessMaxRaw'",
        )
    }

    val historyEnabledRaw = env["FORGE_HISTORY_ENABLED"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val historyEnabled = when (historyEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_HISTORY_ENABLED must be true|false, got '$historyEnabledRaw'",
        )
    }

    val startupAdoptRaw = env["FORGE_STARTUP_ADOPT_LABELS"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val startupAdoptLabels = when (startupAdoptRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_STARTUP_ADOPT_LABELS must be true|false, got '$startupAdoptRaw'",
        )
    }

    val schedulerEnabledRaw = env["FORGE_SCHEDULER_ENABLED"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val schedulerEnabled = when (schedulerEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_SCHEDULER_ENABLED must be true|false, got '$schedulerEnabledRaw'",
        )
    }

    val schedulerStrategy = env["FORGE_SCHEDULER_STRATEGY"]?.trim().orEmpty()
        .ifEmpty { "single-node" }
    if (schedulerStrategy != "single-node") {
        throw IllegalArgumentException(
            "FORGE_SCHEDULER_STRATEGY must be single-node, got '$schedulerStrategy'",
        )
    }

    val schedulerLocalNodeId = env["FORGE_SCHEDULER_LOCAL_NODE_ID"]?.trim().orEmpty()
        .ifEmpty { "node-local" }
    if (schedulerLocalNodeId.isBlank()) {
        throw IllegalArgumentException("FORGE_SCHEDULER_LOCAL_NODE_ID must not be blank")
    }

    return AppConfig(
        port = port,
        serviceName = env["FORGE_SERVICE_NAME"]?.trim().orEmpty().ifEmpty { "forge-control" },
        serviceVersion = env["FORGE_SERVICE_VERSION"]?.trim().orEmpty().ifEmpty { "0.1.0" },
        logLevel = level,
        otelEnabled = otelEnabled,
        otlpEndpoint = env["OTEL_EXPORTER_OTLP_ENDPOINT"]?.trim().orEmpty()
            .ifEmpty { "http://otel-collector:4317" },
        env = env["FORGE_ENV"]?.trim().orEmpty().ifEmpty { "development" },
        authMode = env["FORGE_AUTH_MODE"]?.trim().orEmpty().ifEmpty { "dev" },
        shutdownGraceSeconds = grace,
        database = DatabaseConfig(
            url = env["DATABASE_URL"]?.trim().orEmpty()
                .ifEmpty { "jdbc:postgresql://127.0.0.1:5001/forge" },
            user = env["DATABASE_USER"]?.trim().orEmpty().ifEmpty { "forge" },
            password = env["DATABASE_PASSWORD"]?.trim().orEmpty().ifEmpty { "forge" },
            schema = schema,
            poolMax = poolMax,
            migrateOnStart = migrateOnStart,
        ),
        reconcileEnabled = reconcileEnabled,
        reconcileIntervalMs = reconcileIntervalMs,
        reconcileMaxActionsPerTick = reconcileMaxActionsPerTick,
        runtimeUrl = runtimeUrl,
        gatewayUrl = gatewayUrl,
        rolloutBatchSizeOverride = rolloutBatchSizeOverride,
        rolloutTimeoutOverride = rolloutTimeoutOverride,
        rollbackEnabled = rollbackEnabled,
        readinessPollMs = readinessPollMs,
        readinessMaxWaitSeconds = readinessMaxWaitSeconds,
        historyEnabled = historyEnabled,
        startupAdoptLabels = startupAdoptLabels,
        schedulerEnabled = schedulerEnabled,
        schedulerStrategy = schedulerStrategy,
        schedulerLocalNodeId = schedulerLocalNodeId,
    )
}
