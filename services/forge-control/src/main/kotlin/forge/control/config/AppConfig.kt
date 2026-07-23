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
    val identityUrl: String = "http://forge-identity:4002",
    val introspectCacheTtlS: Long = 10,
    val authzCacheTtlS: Long = 10,
    val shutdownGraceSeconds: Int,
    val database: DatabaseConfig,
    val reconcileEnabled: Boolean = true,
    val reconcileIntervalMs: Long = 2_000,
    val reconcileMaxActionsPerTick: Int = 5,
    val runtimeUrl: String = "http://forge-runtime:4102",
    val gatewayUrl: String = "http://forge-gateway:4000",
    /** Base URL for forge-secrets resolve (empty disables injection). */
    val secretsUrl: String = "http://forge-secrets:8080",
    /** Bearer token Control uses to resolve env bundles (service account). */
    val secretsServiceAccount: String = "",
    /** When true, Control never logs injected secret values (names/fingerprint only). */
    val injectMaskInLogs: Boolean = true,
    val rolloutBatchSizeOverride: Int? = null,
    val rolloutTimeoutOverride: Int? = null,
    val rollbackEnabled: Boolean = true,
    val readinessPollMs: Long = 1_000,
    val readinessMaxWaitSeconds: Long = 60,
    val historyEnabled: Boolean = true,
    val startupAdoptLabels: Boolean = true,
    val schedulerEnabled: Boolean = true,
    val schedulerStrategy: String = "least-allocated",
    val schedulerLocalNodeId: String = "node-local",
    val nodeHeartbeatTimeoutSeconds: Long = 15,
    val livenessIntervalMs: Long = 5_000,
    val nodeStrictRegister: Boolean = false,
    val antiAffinityDefault: String = "soft",
    val queueRetryMs: Long = 2_000,
    val queueMaxLen: Int = 1000,
    val rescheduleEnabled: Boolean = true,
    val rescheduleGraceSeconds: Long = 5,
    /** Managed DB provisioner: `fake` (CI default) or `local` (real Docker provisioner). */
    val dbProvisioner: String = "fake",
    /** Docker network for product Postgres containers. */
    val dbManagedNetwork: String = "forge-net",
    /** Postgres image for LocalProvisioner. */
    val dbPostgresImage: String = "postgres:16",
    /** Host clients use to reach published product DB ports (usually 127.0.0.1). */
    val dbEndpointHost: String = "127.0.0.1",
    /** Default env var name when attaching a managed database (e.g. DATABASE_URL). */
    val dbDefaultEnvVar: String = "DATABASE_URL",
    /** Backup archive target: `storage` or `volume` (storage when available). */
    val dbBackupTarget: String = "volume",
    /** Forge Storage bucket for backups when target=storage. */
    val dbBackupBucket: String = "db-backups",
    /** Local volume directory for backups when target=volume. */
    val dbBackupDir: String = "/var/forge/db-backups",
    /** Base URL for forge-storage (empty disables storage-backed backups). */
    val storageUrl: String = "",
    /** Seconds to keep old credentials valid after new secrets are delivered (rotation). */
    val dbRotationGraceSeconds: Long = 60,
    /** Take a safety backup before forced deletes. */
    val dbPredeleteBackup: Boolean = true,
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

    val secretsUrlRaw = env["FORGE_SECRETS_URL"]?.trim().orEmpty()
    val secretsUrl = if (secretsUrlRaw.isEmpty()) {
        "http://forge-secrets:8080"
    } else if (secretsUrlRaw.equals("disabled", ignoreCase = true) || secretsUrlRaw == "-") {
        ""
    } else {
        secretsUrlRaw
    }
    if (secretsUrl.isNotEmpty() &&
        !secretsUrl.startsWith("http://") &&
        !secretsUrl.startsWith("https://")
    ) {
        throw IllegalArgumentException(
            "FORGE_SECRETS_URL must be an http(s) URL, 'disabled', or empty, got '$secretsUrlRaw'",
        )
    }
    val secretsServiceAccount = env["FORGE_SECRETS_SERVICE_ACCOUNT"]?.trim().orEmpty()
    val injectMaskRaw = env["FORGE_INJECT_MASK_IN_LOGS"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val injectMaskInLogs = when (injectMaskRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_INJECT_MASK_IN_LOGS must be true|false, got '$injectMaskRaw'",
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
        .ifEmpty { "least-allocated" }
    if (schedulerStrategy !in setOf("first-fit", "least-allocated", "single-node")) {
        throw IllegalArgumentException(
            "FORGE_SCHEDULER_STRATEGY must be first-fit|least-allocated|single-node, got '$schedulerStrategy'",
        )
    }

    val schedulerLocalNodeId = env["FORGE_SCHEDULER_LOCAL_NODE_ID"]?.trim().orEmpty()
        .ifEmpty { "node-local" }
    if (schedulerLocalNodeId.isBlank()) {
        throw IllegalArgumentException("FORGE_SCHEDULER_LOCAL_NODE_ID must not be blank")
    }

    val heartbeatTimeoutRaw = env["FORGE_NODE_HEARTBEAT_TIMEOUT_S"]?.trim().orEmpty()
        .ifEmpty { "15" }
    val nodeHeartbeatTimeoutSeconds = heartbeatTimeoutRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_NODE_HEARTBEAT_TIMEOUT_S must be a positive integer, got '$heartbeatTimeoutRaw'",
        )
    if (nodeHeartbeatTimeoutSeconds < 1) {
        throw IllegalArgumentException(
            "FORGE_NODE_HEARTBEAT_TIMEOUT_S must be a positive integer, got '$heartbeatTimeoutRaw'",
        )
    }

    val livenessIntervalRaw = env["FORGE_LIVENESS_INTERVAL_MS"]?.trim().orEmpty()
        .ifEmpty { "5000" }
    val livenessIntervalMs = livenessIntervalRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_LIVENESS_INTERVAL_MS must be a positive integer, got '$livenessIntervalRaw'",
        )
    if (livenessIntervalMs < 1) {
        throw IllegalArgumentException(
            "FORGE_LIVENESS_INTERVAL_MS must be a positive integer, got '$livenessIntervalRaw'",
        )
    }

    val strictRegisterRaw = env["FORGE_NODE_STRICT_REGISTER"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "false" }
    val nodeStrictRegister = when (strictRegisterRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_NODE_STRICT_REGISTER must be true|false, got '$strictRegisterRaw'",
        )
    }

    val antiAffinityDefault = env["FORGE_ANTI_AFFINITY_DEFAULT"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "soft" }
    if (antiAffinityDefault !in setOf("soft", "hard")) {
        throw IllegalArgumentException(
            "FORGE_ANTI_AFFINITY_DEFAULT must be soft|hard, got '$antiAffinityDefault'",
        )
    }

    val queueRetryRaw = env["FORGE_QUEUE_RETRY_MS"]?.trim().orEmpty()
        .ifEmpty { env["FORGE_RECONCILE_INTERVAL_MS"]?.trim().orEmpty().ifEmpty { "2000" } }
    val queueRetryMs = queueRetryRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_QUEUE_RETRY_MS must be a positive integer, got '$queueRetryRaw'",
        )
    if (queueRetryMs < 1) {
        throw IllegalArgumentException(
            "FORGE_QUEUE_RETRY_MS must be a positive integer, got '$queueRetryRaw'",
        )
    }

    val queueMaxRaw = env["FORGE_QUEUE_MAX_LEN"]?.trim().orEmpty().ifEmpty { "1000" }
    val queueMaxLen = queueMaxRaw.toIntOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_QUEUE_MAX_LEN must be a positive integer, got '$queueMaxRaw'",
        )
    if (queueMaxLen < 1) {
        throw IllegalArgumentException(
            "FORGE_QUEUE_MAX_LEN must be a positive integer, got '$queueMaxRaw'",
        )
    }

    val rescheduleEnabledRaw = env["FORGE_RESCHEDULE_ENABLED"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
    val rescheduleEnabled = when (rescheduleEnabledRaw) {
        "true", "1", "yes" -> true
        "false", "0", "no" -> false
        else -> throw IllegalArgumentException(
            "FORGE_RESCHEDULE_ENABLED must be true|false, got '$rescheduleEnabledRaw'",
        )
    }

    val rescheduleGraceRaw = env["FORGE_RESCHEDULE_GRACE_S"]?.trim().orEmpty().ifEmpty { "5" }
    val rescheduleGraceSeconds = rescheduleGraceRaw.toLongOrNull()
        ?: throw IllegalArgumentException(
            "FORGE_RESCHEDULE_GRACE_S must be a non-negative integer, got '$rescheduleGraceRaw'",
        )
    if (rescheduleGraceSeconds < 0) {
        throw IllegalArgumentException(
            "FORGE_RESCHEDULE_GRACE_S must be a non-negative integer, got '$rescheduleGraceRaw'",
        )
    }

    val dbProvisioner = env["FORGE_DB_PROVISIONER"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "fake" }
    if (dbProvisioner !in setOf("fake", "local")) {
        throw IllegalArgumentException(
            "FORGE_DB_PROVISIONER must be fake|local, got '$dbProvisioner'",
        )
    }
    val dbManagedNetwork = env["FORGE_DB_MANAGED_NETWORK"]?.trim().orEmpty()
        .ifEmpty { "forge-net" }
    if (dbManagedNetwork.isBlank()) {
        throw IllegalArgumentException("FORGE_DB_MANAGED_NETWORK must not be blank")
    }
    val dbPostgresImage = env["FORGE_DB_POSTGRES_IMAGE"]?.trim().orEmpty()
        .ifEmpty { "postgres:16" }
    if (dbPostgresImage.isBlank()) {
        throw IllegalArgumentException("FORGE_DB_POSTGRES_IMAGE must not be blank")
    }
    val dbEndpointHost = env["FORGE_DB_ENDPOINT_HOST"]?.trim().orEmpty()
        .ifEmpty { "127.0.0.1" }
    if (dbEndpointHost.isBlank()) {
        throw IllegalArgumentException("FORGE_DB_ENDPOINT_HOST must not be blank")
    }
    val dbDefaultEnvVar = env["FORGE_DB_DEFAULT_ENV_VAR"]?.trim().orEmpty()
        .ifEmpty { "DATABASE_URL" }
    if (!Regex("^[A-Za-z_][A-Za-z0-9_]*$").matches(dbDefaultEnvVar)) {
        throw IllegalArgumentException(
            "FORGE_DB_DEFAULT_ENV_VAR must match [A-Za-z_][A-Za-z0-9_]*, got '$dbDefaultEnvVar'",
        )
    }
    val storageUrl = env["FORGE_STORAGE_URL"]?.trim().orEmpty()
        .let { if (it.equals("disabled", ignoreCase = true)) "" else it }
    val dbBackupTargetRaw = env["FORGE_DB_BACKUP_TARGET"]?.trim()?.lowercase().orEmpty()
    val dbBackupTarget = when {
        dbBackupTargetRaw.isNotEmpty() -> dbBackupTargetRaw
        storageUrl.isNotBlank() -> "storage"
        else -> "volume"
    }
    if (dbBackupTarget !in setOf("storage", "volume")) {
        throw IllegalArgumentException(
            "FORGE_DB_BACKUP_TARGET must be storage|volume, got '$dbBackupTarget'",
        )
    }
    val dbBackupBucket = env["FORGE_DB_BACKUP_BUCKET"]?.trim().orEmpty()
        .ifEmpty { "db-backups" }
    if (dbBackupBucket.isBlank()) {
        throw IllegalArgumentException("FORGE_DB_BACKUP_BUCKET must not be blank")
    }
    val dbBackupDir = env["FORGE_DB_BACKUP_DIR"]?.trim().orEmpty()
        .ifEmpty { "/var/forge/db-backups" }
    if (dbBackupDir.isBlank()) {
        throw IllegalArgumentException("FORGE_DB_BACKUP_DIR must not be blank")
    }
    val dbRotationGraceSeconds = env["FORGE_DB_ROTATION_GRACE_SECONDS"]?.trim().orEmpty()
        .ifEmpty { "60" }
        .toLongOrNull()
        ?.takeIf { it >= 0 }
        ?: throw IllegalArgumentException(
            "FORGE_DB_ROTATION_GRACE_SECONDS must be a non-negative integer",
        )
    val dbPredeleteBackup = env["FORGE_DB_PREDELETE_BACKUP"]?.trim()?.lowercase().orEmpty()
        .ifEmpty { "true" }
        .let {
            when (it) {
                "true", "1", "yes" -> true
                "false", "0", "no" -> false
                else -> throw IllegalArgumentException(
                    "FORGE_DB_PREDELETE_BACKUP must be true|false, got '$it'",
                )
            }
        }

    return AppConfig(
        port = port,
        serviceName = env["FORGE_SERVICE_NAME"]?.trim().orEmpty().ifEmpty { "forge-control" },
        serviceVersion = env["FORGE_SERVICE_VERSION"]?.trim().orEmpty().ifEmpty { "0.1.0" },
        logLevel = level,
        otelEnabled = otelEnabled,
        otlpEndpoint = env["FORGE_OTEL_EXPORTER_ENDPOINT"]?.trim().orEmpty()
            .ifEmpty { env["OTEL_EXPORTER_OTLP_ENDPOINT"]?.trim().orEmpty() }
            .ifEmpty { "http://otel-collector:4317" },
        env = env["FORGE_ENV"]?.trim().orEmpty().ifEmpty { "development" },
        authMode = env["FORGE_AUTH_MODE"]?.trim().orEmpty().ifEmpty { "enforce" },
        identityUrl = env["FORGE_IDENTITY_URL"]?.trim().orEmpty()
            .ifEmpty { "http://forge-identity:4002" },
        introspectCacheTtlS = env["FORGE_INTROSPECT_CACHE_TTL_S"]?.trim().orEmpty()
            .ifEmpty { "10" }
            .toLongOrNull()
            ?.takeIf { it >= 0 }
            ?: throw IllegalArgumentException(
                "FORGE_INTROSPECT_CACHE_TTL_S must be a non-negative integer",
            ),
        authzCacheTtlS = env["FORGE_AUTHZ_CACHE_TTL_S"]?.trim().orEmpty()
            .ifEmpty { "10" }
            .toLongOrNull()
            ?.takeIf { it >= 0 }
            ?: throw IllegalArgumentException(
                "FORGE_AUTHZ_CACHE_TTL_S must be a non-negative integer",
            ),
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
        secretsUrl = secretsUrl,
        secretsServiceAccount = secretsServiceAccount,
        injectMaskInLogs = injectMaskInLogs,
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
        nodeHeartbeatTimeoutSeconds = nodeHeartbeatTimeoutSeconds,
        livenessIntervalMs = livenessIntervalMs,
        nodeStrictRegister = nodeStrictRegister,
        antiAffinityDefault = antiAffinityDefault,
        queueRetryMs = queueRetryMs,
        queueMaxLen = queueMaxLen,
        rescheduleEnabled = rescheduleEnabled,
        rescheduleGraceSeconds = rescheduleGraceSeconds,
        dbProvisioner = dbProvisioner,
        dbManagedNetwork = dbManagedNetwork,
        dbPostgresImage = dbPostgresImage,
        dbEndpointHost = dbEndpointHost,
        dbDefaultEnvVar = dbDefaultEnvVar,
        dbBackupTarget = dbBackupTarget,
        dbBackupBucket = dbBackupBucket,
        dbBackupDir = dbBackupDir,
        storageUrl = storageUrl,
        dbRotationGraceSeconds = dbRotationGraceSeconds,
        dbPredeleteBackup = dbPredeleteBackup,
    )
}
