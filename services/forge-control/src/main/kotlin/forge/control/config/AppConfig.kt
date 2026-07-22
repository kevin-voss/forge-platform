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
    )
}
