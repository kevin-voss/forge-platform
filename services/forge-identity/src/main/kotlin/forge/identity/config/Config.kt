package forge.identity.config

data class DatabaseConfig(
    val url: String,
    val user: String,
    val password: String,
    val poolMax: Int,
    val migrateOnStart: Boolean,
    val connectRetryInitialMs: Long,
    val connectRetryMaxMs: Long,
)

data class Config(
    val port: Int,
    val serviceName: String,
    val serviceVersion: String,
    val logLevel: String,
    val env: String,
    val shutdownGraceSeconds: Int,
    val database: DatabaseConfig,
    /** Optional bootstrap admin email (`FORGE_IDENTITY_SEED_ADMIN`). */
    val seedAdminEmail: String? = null,
)

fun loadConfig(env: Map<String, String> = System.getenv()): Config {
    val portRaw = env["PORT"]?.trim().orEmpty().ifEmpty { "4002" }
    val port = portRaw.toIntOrNull()
        ?: throw IllegalArgumentException("PORT must be an integer 1–65535, got '$portRaw'")
    if (port !in 1..65535) {
        throw IllegalArgumentException("PORT must be an integer 1–65535, got '$portRaw'")
    }

    val level = env["FORGE_LOG_LEVEL"]?.trim()?.lowercase().orEmpty().ifEmpty { "info" }
    if (level !in setOf("debug", "info", "warn", "error")) {
        throw IllegalArgumentException("FORGE_LOG_LEVEL must be debug|info|warn|error, got '$level'")
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

    val dbUrl = env["FORGE_IDENTITY_DB_URL"]?.trim().orEmpty()
    if (dbUrl.isEmpty()) {
        throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_URL is required (e.g. jdbc:postgresql://postgres:5432/forge_identity)",
        )
    }

    val poolRaw = env["FORGE_IDENTITY_DB_POOL_MAX"]?.trim().orEmpty().ifEmpty { "10" }
    val poolMax = poolRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_POOL_MAX must be a positive integer, got '$poolRaw'",
        )
    if (poolMax < 1) {
        throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_POOL_MAX must be a positive integer, got '$poolRaw'",
        )
    }

    val migrateRaw = env["FORGE_IDENTITY_DB_MIGRATE_ON_START"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val migrateOnStart = when (migrateRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_MIGRATE_ON_START must be true|false, got '$migrateRaw'",
        )
    }

    val retryInitialRaw = env["FORGE_IDENTITY_DB_RETRY_INITIAL_MS"]?.trim().orEmpty()
        .ifEmpty { "500" }
    val connectRetryInitialMs = retryInitialRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_RETRY_INITIAL_MS must be a positive integer, got '$retryInitialRaw'",
        )
    if (connectRetryInitialMs < 1) {
        throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_RETRY_INITIAL_MS must be a positive integer, got '$retryInitialRaw'",
        )
    }

    val retryMaxRaw = env["FORGE_IDENTITY_DB_RETRY_MAX_MS"]?.trim().orEmpty().ifEmpty { "5000" }
    val connectRetryMaxMs = retryMaxRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_RETRY_MAX_MS must be a positive integer, got '$retryMaxRaw'",
        )
    if (connectRetryMaxMs < connectRetryInitialMs) {
        throw IllegalArgumentException(
            "FORGE_IDENTITY_DB_RETRY_MAX_MS must be >= FORGE_IDENTITY_DB_RETRY_INITIAL_MS",
        )
    }

    val seedAdmin = env["FORGE_IDENTITY_SEED_ADMIN"]?.trim().orEmpty().ifEmpty { null }
    if (seedAdmin != null && !seedAdmin.contains('@')) {
        throw IllegalArgumentException(
            "FORGE_IDENTITY_SEED_ADMIN must be an email address when set, got '$seedAdmin'",
        )
    }

    return Config(
        port = port,
        serviceName = env["FORGE_SERVICE_NAME"]?.trim().orEmpty().ifEmpty { "forge-identity" },
        serviceVersion = env["FORGE_SERVICE_VERSION"]?.trim().orEmpty().ifEmpty { "0.1.0" },
        logLevel = level,
        env = env["FORGE_ENV"]?.trim().orEmpty().ifEmpty { "development" },
        shutdownGraceSeconds = grace,
        database = DatabaseConfig(
            url = dbUrl,
            user = env["FORGE_IDENTITY_DB_USER"]?.trim().orEmpty().ifEmpty { "forge" },
            password = env["FORGE_IDENTITY_DB_PASSWORD"]?.trim().orEmpty().ifEmpty { "forge" },
            poolMax = poolMax,
            migrateOnStart = migrateOnStart,
            connectRetryInitialMs = connectRetryInitialMs,
            connectRetryMaxMs = connectRetryMaxMs,
        ),
        seedAdminEmail = seedAdmin,
    )
}
