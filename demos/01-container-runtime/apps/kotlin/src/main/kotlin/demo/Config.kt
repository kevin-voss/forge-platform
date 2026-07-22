package demo

data class Config(
    val port: Int,
    val serviceName: String,
    val serviceVersion: String,
    val logLevel: String,
    val env: String,
)

fun loadConfig(env: Map<String, String> = System.getenv()): Config {
    val portRaw = env["PORT"]?.trim().orEmpty()
    if (portRaw.isEmpty()) {
        throw IllegalArgumentException("PORT is required")
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

    val name = env["FORGE_SERVICE_NAME"]?.trim().orEmpty().ifEmpty { "demo-kotlin-api" }
    val version = env["FORGE_SERVICE_VERSION"]?.trim().orEmpty().ifEmpty { "0.1.0" }
    val forgeEnv = env["FORGE_ENV"]?.trim().orEmpty().ifEmpty { "development" }

    return Config(
        port = port,
        serviceName = name,
        serviceVersion = version,
        logLevel = level,
        env = forgeEnv,
    )
}
