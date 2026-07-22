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

data class AuthConfig(
    /** Fixed session lifetime in seconds (`FORGE_SESSION_TTL_S`). */
    val sessionTtlSeconds: Long = 86_400,
    /** Argon2id memory in KiB (`FORGE_ARGON2_MEMORY_KB`). */
    val argon2MemoryKb: Int = 65_536,
    /** Argon2id iterations (`FORGE_ARGON2_ITERATIONS`). */
    val argon2Iterations: Int = 3,
    val argon2Parallelism: Int = 1,
    /** Failed logins before lockout (`FORGE_LOGIN_MAX_FAILS`). */
    val loginMaxFails: Int = 5,
    /** Lockout window in seconds (default 15 minutes). */
    val loginLockoutWindowSeconds: Long = 900,
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
    val auth: AuthConfig = AuthConfig(),
    /** Informational matrix version (`FORGE_AUTHZ_MATRIX_VERSION`); matrix is code-defined. */
    val authzMatrixVersion: String = "1",
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

    val sessionTtlRaw = env["FORGE_SESSION_TTL_S"]?.trim().orEmpty().ifEmpty { "86400" }
    val sessionTtl = sessionTtlRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_SESSION_TTL_S must be a positive integer, got '$sessionTtlRaw'",
        )
    if (sessionTtl < 1) {
        throw IllegalArgumentException(
            "FORGE_SESSION_TTL_S must be a positive integer, got '$sessionTtlRaw'",
        )
    }

    val argonMemRaw = env["FORGE_ARGON2_MEMORY_KB"]?.trim().orEmpty().ifEmpty { "65536" }
    val argonMem = argonMemRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_ARGON2_MEMORY_KB must be a positive integer, got '$argonMemRaw'",
        )
    if (argonMem < 8) {
        throw IllegalArgumentException(
            "FORGE_ARGON2_MEMORY_KB must be >= 8, got '$argonMemRaw'",
        )
    }

    val argonIterRaw = env["FORGE_ARGON2_ITERATIONS"]?.trim().orEmpty().ifEmpty { "3" }
    val argonIter = argonIterRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_ARGON2_ITERATIONS must be a positive integer, got '$argonIterRaw'",
        )
    if (argonIter < 1) {
        throw IllegalArgumentException(
            "FORGE_ARGON2_ITERATIONS must be a positive integer, got '$argonIterRaw'",
        )
    }

    val loginMaxRaw = env["FORGE_LOGIN_MAX_FAILS"]?.trim().orEmpty().ifEmpty { "5" }
    val loginMaxFails = loginMaxRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_LOGIN_MAX_FAILS must be a positive integer, got '$loginMaxRaw'",
        )
    if (loginMaxFails < 1) {
        throw IllegalArgumentException(
            "FORGE_LOGIN_MAX_FAILS must be a positive integer, got '$loginMaxRaw'",
        )
    }

    val matrixVersion = env["FORGE_AUTHZ_MATRIX_VERSION"]?.trim().orEmpty().ifEmpty { "1" }

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
        auth = AuthConfig(
            sessionTtlSeconds = sessionTtl,
            argon2MemoryKb = argonMem,
            argon2Iterations = argonIter,
            loginMaxFails = loginMaxFails,
        ),
        authzMatrixVersion = matrixVersion,
    )
}
