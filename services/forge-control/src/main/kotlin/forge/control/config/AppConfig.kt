package forge.control.config

data class AppConfig(
    val port: Int,
    val serviceName: String,
    val serviceVersion: String,
    val logLevel: String,
    val env: String,
    val authMode: String,
    val shutdownGraceSeconds: Int,
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

    return AppConfig(
        port = port,
        serviceName = env["FORGE_SERVICE_NAME"]?.trim().orEmpty().ifEmpty { "forge-control" },
        serviceVersion = env["FORGE_SERVICE_VERSION"]?.trim().orEmpty().ifEmpty { "0.1.0" },
        logLevel = level,
        env = env["FORGE_ENV"]?.trim().orEmpty().ifEmpty { "development" },
        authMode = env["FORGE_AUTH_MODE"]?.trim().orEmpty().ifEmpty { "dev" },
        shutdownGraceSeconds = grace,
    )
}
