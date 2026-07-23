package forge.control

import forge.control.auth.AuthMiddleware
import forge.control.auth.AuthzCache
import forge.control.auth.HttpIdentityClient
import forge.control.auth.IntrospectionCache
import forge.control.auth.MapProjectScopeResolver
import forge.control.auth.RepositoryProjectScopeResolver
import forge.control.auth.RouteActionMap
import forge.control.auth.installAuthMiddleware
import forge.control.config.AppConfig
import forge.control.config.loadAppConfig
import forge.control.db.Db
import forge.control.http.AlwaysHealthyDb
import forge.control.http.ApiException
import forge.control.http.DbProbe
import forge.control.http.Readiness
import forge.control.http.applicationRoutes
import forge.control.http.apiError
import forge.control.http.deploymentRoutes
import forge.control.http.environmentRoutes
import forge.control.http.healthRoutes
import forge.control.http.projectRoutes
import forge.control.http.historyRoutes
import forge.control.http.reconcileStatusRoutes
import forge.control.http.serviceRoutes
import forge.control.http.toEnvelope
import forge.control.http.installRequestId
import forge.control.logging.JsonLog
import forge.control.manageddb.BackupRunner
import forge.control.manageddb.FakeProvisioner
import forge.control.manageddb.HttpManagedDbSecretsClient
import forge.control.manageddb.InMemoryManagedDbSecretsClient
import forge.control.manageddb.IsolationGuard
import forge.control.manageddb.JdbcManagedDbRepository
import forge.control.manageddb.LocalProvisioner
import forge.control.manageddb.ManagedDbService
import forge.control.manageddb.Provisioner
import forge.control.manageddb.RestoreRunner
import forge.control.manageddb.buildArchiveStore
import forge.control.manageddb.managedDbRoutes
import java.nio.file.Path
import forge.control.reconcile.HttpGatewayClient
import forge.control.reconcile.HttpRuntimeClient
import forge.control.reconcile.JdbcDeploymentHistory
import forge.control.reconcile.JdbcLastHealthyStore
import forge.control.reconcile.JdbcReconcileStatusStore
import forge.control.reconcile.JdbcTransitionRecorder
import forge.control.reconcile.PlacementAwareRuntimeClient
import forge.control.reconcile.ReadinessGate
import forge.control.reconcile.ReconciliationController
import forge.control.reconcile.RepositoryDeploymentStore
import forge.control.reconcile.RuntimeClient
import forge.control.reconcile.StartupRecovery
import forge.control.reconcile.TrafficShifter
import forge.control.scheduler.CapacityReservation
import forge.control.scheduler.JdbcNodeStore
import forge.control.scheduler.JdbcPlacementStore
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeOfflineHandler
import forge.control.scheduler.PendingQueue
import forge.control.scheduler.PlacementService
import forge.control.scheduler.QueueProcessor
import forge.control.scheduler.SchedulerFactory
import forge.control.scheduler.StaleReplicaFencer
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.api.nodeFleetRoutes
import forge.control.scheduler.api.nodeRegistrationRoutes
import forge.control.scheduler.api.placementRoutes
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.repo.JdbcIdempotencyStore
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import forge.control.telemetry.Telemetry
import forge.control.telemetry.TelemetryConfig
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationCallPipeline
import io.ktor.server.application.ApplicationStarted
import io.ktor.server.application.call
import io.ktor.server.application.install
import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty
import io.ktor.server.plugins.calllogging.CallLogging
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation
import io.ktor.server.plugins.statuspages.StatusPages
import io.ktor.server.request.httpMethod
import io.ktor.server.request.path
import io.ktor.server.response.respond
import io.ktor.server.routing.routing
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import org.slf4j.event.Level
import java.time.Duration
import kotlin.system.exitProcess

fun main() {
    val cfg = try {
        loadAppConfig()
    } catch (e: IllegalArgumentException) {
        System.err.println("fatal: ${e.message}")
        exitProcess(1)
    }

    val log = JsonLog(cfg.serviceName, cfg.logLevel)
    val telemetry = Telemetry.initialize(
        TelemetryConfig(
            enabled = cfg.otelEnabled,
            serviceName = cfg.serviceName,
            otlpEndpoint = cfg.otlpEndpoint,
            environment = cfg.env,
        ),
    )
    val db = try {
        Db.open(cfg.database, log)
    } catch (e: Exception) {
        log.error("db pool failed to initialize", "error" to (e.message ?: e.javaClass.simpleName))
        exitProcess(1)
    }

    if (cfg.database.migrateOnStart) {
        try {
            val result = db.migrate()
            log.info(
                "migrations applied",
                "from" to result.initialSchemaVersion,
                "to" to result.targetSchemaVersion,
                "migrations_executed" to result.migrationsExecuted,
            )
        } catch (e: Exception) {
            log.error("migration failed", "error" to (e.message ?: e.javaClass.simpleName))
            db.close()
            exitProcess(1)
        }
    }

    // Creates attributed to the configured auth mode actor (dev bypass or enforce).
    val actor = if (cfg.authMode.equals("dev", ignoreCase = true)) "dev" else "system"
    if (cfg.authMode.equals("dev", ignoreCase = true)) {
        log.warn(
            "FORGE_AUTH_MODE=dev is enabled — authentication and authorization are BYPASSED. " +
                "This is insecure; set FORGE_AUTH_MODE=enforce (default) for production-shaped runs.",
        )
    }
    val projectRepo = JdbcProjectRepository(db.dataSource)
    val environmentRepo = JdbcEnvironmentRepository(db.dataSource)
    val applicationRepo = JdbcApplicationRepository(db.dataSource)
    val serviceRepo = JdbcServiceRepository(db.dataSource)
    val deploymentRepo = JdbcDeploymentRepository(db.dataSource)
    val auditRepo = JdbcAuditRepository(db.dataSource)
    val managedDbRepo = JdbcManagedDbRepository(db.dataSource)
    val relationships = RelationshipValidator(projectRepo, applicationRepo)
    val isolationGuard = IsolationGuard(
        controlJdbcUrl = cfg.database.url,
        controlUser = cfg.database.user,
    )
    val dbSecretsClient = if (cfg.secretsUrl.isBlank()) {
        InMemoryManagedDbSecretsClient()
    } else {
        HttpManagedDbSecretsClient(
            secretsUrl = cfg.secretsUrl,
            serviceAccountToken = cfg.secretsServiceAccount,
        )
    }
    val dbProvisioner: Provisioner = when (cfg.dbProvisioner) {
        "local" -> LocalProvisioner(
            isolation = isolationGuard,
            network = cfg.dbManagedNetwork,
            image = cfg.dbPostgresImage,
            endpointHost = cfg.dbEndpointHost,
            log = log,
        )
        else -> FakeProvisioner(isolationGuard)
    }
    val archiveStore = buildArchiveStore(
        target = cfg.dbBackupTarget,
        backupDir = Path.of(cfg.dbBackupDir),
        storageUrl = cfg.storageUrl,
        bucket = cfg.dbBackupBucket,
        serviceToken = cfg.secretsServiceAccount,
    )
    val backupRunner = BackupRunner(
        store = managedDbRepo,
        provisioner = dbProvisioner,
        archives = archiveStore,
        log = log,
        telemetry = telemetry,
    )
    val restoreRunner = RestoreRunner(
        store = managedDbRepo,
        provisioner = dbProvisioner,
        archives = archiveStore,
        log = log,
        telemetry = telemetry,
    )
    val managedDbService = ManagedDbService(
        store = managedDbRepo,
        provisioner = dbProvisioner,
        isolation = isolationGuard,
        relationships = relationships,
        secrets = dbSecretsClient,
        applications = applicationRepo,
        defaultEnvVar = cfg.dbDefaultEnvVar,
        backupRunner = backupRunner,
        restoreRunner = restoreRunner,
        archives = archiveStore,
        log = log,
        telemetry = telemetry,
    )
    val deploymentStore = RepositoryDeploymentStore(
        deploymentRepo,
        serviceRepo,
        applications = applicationRepo,
        environments = environmentRepo,
        rolloutBatchSizeOverride = cfg.rolloutBatchSizeOverride,
        rolloutTimeoutOverride = cfg.rolloutTimeoutOverride,
    )
    val placementStore = JdbcPlacementStore(db.dataSource)
    val nodeStore = JdbcNodeStore(db.dataSource)
    val runtimeClient: RuntimeClient = PlacementAwareRuntimeClient(
        fallback = HttpRuntimeClient(cfg.runtimeUrl),
        nodeStore = nodeStore,
        placementStore = placementStore,
    )
    val readinessGate = ReadinessGate(
        runtimeClient = runtimeClient,
        pollMs = cfg.readinessPollMs,
        maxWaitSeconds = cfg.readinessMaxWaitSeconds,
    )
    val trafficShifter = TrafficShifter(HttpGatewayClient(cfg.gatewayUrl))
    val reconcileStatusStore = JdbcReconcileStatusStore(db.dataSource)
    val lastHealthyStore = JdbcLastHealthyStore(db.dataSource)
    val deploymentHistory = JdbcDeploymentHistory(db.dataSource)
    val transitionRecorder = JdbcTransitionRecorder(
        dataSource = db.dataSource,
        history = deploymentHistory,
        log = log,
        enabled = cfg.historyEnabled,
        telemetry = telemetry,
    )
    val capacityReservation = CapacityReservation(nodeStore)
    val placementScheduler = SchedulerFactory.create(
        strategy = cfg.schedulerStrategy,
        nodeStore = nodeStore,
        reservation = capacityReservation,
        localNodeId = cfg.schedulerLocalNodeId,
        schedulerEnabled = cfg.schedulerEnabled,
        placementStore = placementStore,
        telemetry = telemetry,
    )
    val pendingQueue = if (cfg.schedulerEnabled) {
        PendingQueue(store = placementStore, maxLen = cfg.queueMaxLen)
    } else {
        null
    }
    val queueProcessor = if (pendingQueue != null) {
        QueueProcessor(
            queue = pendingQueue,
            scheduler = placementScheduler,
            store = placementStore,
            log = log,
            intervalMs = cfg.queueRetryMs,
            telemetry = telemetry,
        )
    } else {
        null
    }
    val placementService = PlacementService(
        scheduler = placementScheduler,
        store = placementStore,
        log = log,
        telemetry = telemetry,
        reservation = capacityReservation,
        pendingQueue = pendingQueue,
        queueProcessor = queueProcessor,
        defaultAntiAffinity = AntiAffinity.parse(cfg.antiAffinityDefault),
    )
    val nodeOfflineHandler = if (cfg.schedulerEnabled) {
        NodeOfflineHandler(
            store = placementStore,
            placementService = placementService,
            reservation = capacityReservation,
            deploymentStore = deploymentStore,
            log = log,
            enabled = cfg.rescheduleEnabled,
            grace = Duration.ofSeconds(cfg.rescheduleGraceSeconds),
            history = deploymentHistory,
            telemetry = telemetry,
            nodeStore = nodeStore,
        )
    } else {
        null
    }
    val staleReplicaFencer = if (cfg.schedulerEnabled && cfg.rescheduleEnabled) {
        StaleReplicaFencer(
            store = placementStore,
            runtimeClient = runtimeClient,
            log = log,
            telemetry = telemetry,
        )
    } else {
        null
    }
    val livenessMonitor = LivenessMonitor(
        store = nodeStore,
        timeout = Duration.ofSeconds(cfg.nodeHeartbeatTimeoutSeconds),
        intervalMs = cfg.livenessIntervalMs,
        log = log,
        telemetry = telemetry,
        onStatusTransition = { nodeId, status ->
            nodeOfflineHandler?.onStatusTransition(nodeId, status)
        },
    )
    val secretsClient: forge.control.reconcile.SecretsClient =
        if (cfg.secretsUrl.isNotBlank()) {
            forge.control.reconcile.HttpSecretsClient(
                secretsUrl = cfg.secretsUrl,
                serviceAccountToken = cfg.secretsServiceAccount,
            )
        } else {
            forge.control.reconcile.NoOpSecretsClient
        }
    val reconcileController = ReconciliationController(
        deploymentStore = deploymentStore,
        runtimeClient = runtimeClient,
        statusStore = reconcileStatusStore,
        log = log,
        intervalMs = cfg.reconcileIntervalMs,
        enabled = cfg.reconcileEnabled,
        maxActionsPerTick = cfg.reconcileMaxActionsPerTick,
        telemetry = telemetry,
        readinessGate = readinessGate,
        trafficShifter = trafficShifter,
        readinessMaxWaitSeconds = cfg.readinessMaxWaitSeconds,
        lastHealthyStore = lastHealthyStore,
        rollbackEnabled = cfg.rollbackEnabled,
        transitionRecorder = transitionRecorder,
        placementService = if (cfg.schedulerEnabled) placementService else null,
        staleReplicaFencer = staleReplicaFencer,
        secretsClient = secretsClient,
        injectMaskInLogs = cfg.injectMaskInLogs,
        attachmentEnvSource = managedDbService,
    )
    val startupRecovery = StartupRecovery(
        deploymentStore = deploymentStore,
        runtimeClient = runtimeClient,
        transitionRecorder = transitionRecorder,
        lastHealthyStore = lastHealthyStore,
        log = log,
        adoptLabels = cfg.startupAdoptLabels,
    )
    val services = ControlServices(
        projects = ProjectService(projectRepo, auditRepo, actor = actor),
        environments = EnvironmentService(projectRepo, environmentRepo, auditRepo, actor = actor),
        applications = ApplicationService(applicationRepo, relationships, auditRepo, actor = actor),
        services = ServiceService(serviceRepo, relationships, auditRepo, actor = actor),
        deployments = DeploymentService(
            deploymentRepo,
            serviceRepo,
            applicationRepo,
            environmentRepo,
            auditRepo,
            actor = actor,
        ),
        projectTrees = ProjectTreeService(
            projectRepo,
            environmentRepo,
            applicationRepo,
            serviceRepo,
            deploymentRepo,
        ),
        idempotency = JdbcIdempotencyStore(db.dataSource),
        deploymentStore = deploymentStore,
        runtimeClient = runtimeClient,
        reconcileStatusStore = reconcileStatusStore,
        deploymentHistory = deploymentHistory,
        placementService = placementService,
        nodeStore = nodeStore,
        nodeStrictRegister = cfg.nodeStrictRegister,
        onNodeRegistered = { placementService.drainQueue() },
        managedDb = managedDbService,
    )

    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000
    val dbProbe = DbProbe { db.check() }

    val identityClient = HttpIdentityClient(
        identityUrl = cfg.identityUrl,
        introspectCache = IntrospectionCache(ttlMillis = cfg.introspectCacheTtlS * 1_000),
        authzCache = AuthzCache(ttlMillis = cfg.authzCacheTtlS * 1_000),
    )
    val routeActionMap = RouteActionMap(
        scope = RepositoryProjectScopeResolver(
            applications = applicationRepo,
            services = serviceRepo,
            environments = environmentRepo,
            deployments = deploymentRepo,
            dbInstances = managedDbRepo,
        ),
    )
    val authMiddleware = AuthMiddleware(
        authMode = cfg.authMode,
        identity = identityClient,
        routes = routeActionMap,
        log = log,
    )

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeControlModule(
            cfg = cfg,
            readiness = readiness,
            dbProbe = dbProbe,
            services = services,
            log = log,
            telemetry = telemetry,
            authMiddleware = authMiddleware,
        ) { cause ->
            log.error("readiness db check failed", "error" to cause)
        }
        monitor.subscribe(ApplicationStarted) {
            readiness.markReady()
            try {
                startupRecovery.recover()
            } catch (e: Exception) {
                log.error(
                    "startup recovery failed",
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
            livenessMonitor.start()
            nodeOfflineHandler?.start()
            queueProcessor?.start()
            reconcileController.start()
            log.info(
                "listening",
                "port" to cfg.port,
                "version" to cfg.serviceVersion,
                "env" to cfg.env,
                "auth_mode" to cfg.authMode,
                "reconcile_enabled" to cfg.reconcileEnabled,
                "reconcile_interval_ms" to cfg.reconcileIntervalMs,
                "history_enabled" to cfg.historyEnabled,
                "startup_adopt_labels" to cfg.startupAdoptLabels,
                "scheduler_enabled" to cfg.schedulerEnabled,
                "scheduler_strategy" to cfg.schedulerStrategy,
                "scheduler_local_node_id" to cfg.schedulerLocalNodeId,
                "node_heartbeat_timeout_s" to cfg.nodeHeartbeatTimeoutSeconds,
                "liveness_interval_ms" to cfg.livenessIntervalMs,
                "anti_affinity_default" to cfg.antiAffinityDefault,
                "queue_retry_ms" to cfg.queueRetryMs,
                "queue_max_len" to cfg.queueMaxLen,
                "reschedule_enabled" to cfg.rescheduleEnabled,
                "reschedule_grace_s" to cfg.rescheduleGraceSeconds,
                "db_provisioner" to cfg.dbProvisioner,
                "db_managed_network" to cfg.dbManagedNetwork,
            )
        }
    }

    Runtime.getRuntime().addShutdownHook(
        Thread {
            log.info("shutdown signal received", "signal" to "SIGTERM")
            reconcileController.stop()
            queueProcessor?.stop()
            nodeOfflineHandler?.stop()
            livenessMonitor.stop()
            server.stop(gracePeriodMillis = 1_000, timeoutMillis = graceMillis)
            db.close()
            telemetry.close()
            log.info("shutdown complete")
            // HotSpot reports SIGTERM as 143 unless we halt(0) after a clean drain.
            // Do not call System.exit/exitProcess from a shutdown hook (deadlocks).
            Runtime.getRuntime().halt(0)
        },
    )

    log.info(
        "starting",
        "port" to cfg.port,
        "version" to cfg.serviceVersion,
        "env" to cfg.env,
        "auth_mode" to cfg.authMode,
        "identity_url" to cfg.identityUrl,
        "introspect_cache_ttl_s" to cfg.introspectCacheTtlS,
        "authz_cache_ttl_s" to cfg.authzCacheTtlS,
        "log_level" to cfg.logLevel,
        "otel_enabled" to cfg.otelEnabled,
        "shutdown_grace_seconds" to cfg.shutdownGraceSeconds,
        "db_schema" to cfg.database.schema,
        "db_migrate_on_start" to cfg.database.migrateOnStart,
        "reconcile_enabled" to cfg.reconcileEnabled,
        "reconcile_interval_ms" to cfg.reconcileIntervalMs,
        "runtime_url" to cfg.runtimeUrl,
    )

    try {
        server.start(wait = true)
    } finally {
        reconcileController.stop()
        queueProcessor?.stop()
        nodeOfflineHandler?.stop()
        livenessMonitor.stop()
        db.close()
        telemetry.close()
    }
}

fun Application.forgeControlModule(
    cfg: AppConfig,
    readiness: Readiness,
    dbProbe: DbProbe = AlwaysHealthyDb,
    services: ControlServices? = null,
    log: JsonLog? = null,
    telemetry: Telemetry = Telemetry.current(),
    authMiddleware: AuthMiddleware? = null,
    onDbFailure: (String) -> Unit = {},
) {
    install(ContentNegotiation) {
        json(
            Json {
                encodeDefaults = true
                explicitNulls = false
                ignoreUnknownKeys = true
            },
        )
    }

    install(CallLogging) {
        level = Level.INFO
        filter { call -> !call.request.path().startsWith("/health") }
    }
    installRequestId()

    val resolvedAuth = authMiddleware ?: AuthMiddleware(
        authMode = cfg.authMode,
        identity = HttpIdentityClient(
            identityUrl = cfg.identityUrl,
            introspectCache = IntrospectionCache(ttlMillis = cfg.introspectCacheTtlS * 1_000),
            authzCache = AuthzCache(ttlMillis = cfg.authzCacheTtlS * 1_000),
        ),
        routes = RouteActionMap(MapProjectScopeResolver()),
        log = log,
    )
    if (resolvedAuth.isDevBypass) {
        log?.warn(
            "FORGE_AUTH_MODE=dev — auth bypass active (insecure; opt-in only)",
        )
    }

    install(StatusPages) {
        exception<ApiException> { call, cause ->
            call.respond(cause.status, cause.toEnvelope())
        }
        exception<SerializationException> { call, _ ->
            call.respond(
                HttpStatusCode.BadRequest,
                apiError("invalid_request", "invalid JSON body"),
            )
        }
        exception<IllegalArgumentException> { call, cause ->
            // kotlinx.serialization / receive can surface as IllegalArgumentException
            call.respond(
                HttpStatusCode.BadRequest,
                apiError("invalid_request", cause.message ?: "invalid request"),
            )
        }
        exception<Throwable> { call, cause ->
            call.respond(
                HttpStatusCode.InternalServerError,
                apiError("internal_error", "internal server error"),
            )
            call.application.environment.log.error("unhandled error: ${cause.message}", cause)
        }
        status(HttpStatusCode.NotFound) { call, _ ->
            if (call.response.isCommitted) return@status
            call.respond(
                HttpStatusCode.NotFound,
                apiError("not_found", "resource not found"),
            )
        }
    }

    // After StatusPages so Unauthorized/Forbidden are rendered as ErrorEnvelope.
    installAuthMiddleware(resolvedAuth)

    if (log != null) {
        intercept(ApplicationCallPipeline.Monitoring) {
            val started = System.currentTimeMillis()
            val method = call.request.httpMethod.value
            val path = call.request.path()
            val parent = forge.control.observability.Otel.extract(call.request.headers)
            val (span, scope) = forge.control.observability.Otel.startServerSpan(parent, method, path)
            try {
                proceed()
            } catch (error: Throwable) {
                span.recordException(error)
                span.setStatus(io.opentelemetry.api.trace.StatusCode.ERROR)
                throw error
            } finally {
                val status = call.response.status()?.value ?: 0
                if (!path.startsWith("/health")) {
                    val durationMs = System.currentTimeMillis() - started
                    telemetry.recordRequest(method, status, durationMs)
                    log.info(
                        "request",
                        "method" to method,
                        "path" to path,
                        "status" to status,
                        "duration_ms" to durationMs,
                    )
                }
                forge.control.observability.Otel.finishSpan(span, scope, status)
            }
        }
    }

    routing {
        healthRoutes(readiness, dbProbe, onDbFailure)
        if (services != null) {
            projectRoutes(services.projects, services.projectTrees, services.idempotency)
            environmentRoutes(services.environments, services.idempotency)
            applicationRoutes(services.applications, services.idempotency)
            serviceRoutes(services.services, services.idempotency)
            deploymentRoutes(services.deployments, services.idempotency)
            val deploymentStore = services.deploymentStore
            val runtimeClient = services.runtimeClient
            val reconcileStatusStore = services.reconcileStatusStore
            if (deploymentStore != null && runtimeClient != null && reconcileStatusStore != null) {
                reconcileStatusRoutes(deploymentStore, runtimeClient, reconcileStatusStore)
            }
            val history = services.deploymentHistory
            if (deploymentStore != null && history != null) {
                historyRoutes(deploymentStore, history)
            }
            val placementService = services.placementService
            if (placementService != null) {
                placementRoutes(placementService)
            }
            val nodeStore = services.nodeStore
            if (nodeStore != null) {
                nodeFleetRoutes(nodeStore)
                nodeRegistrationRoutes(
                    store = nodeStore,
                    log = log ?: JsonLog(cfg.serviceName, cfg.logLevel),
                    strictRegister = services.nodeStrictRegister,
                    telemetry = telemetry,
                    onRegistered = services.onNodeRegistered,
                )
            }
            val managedDb = services.managedDb
            if (managedDb != null) {
                managedDbRoutes(managedDb, services.idempotency)
            }
        }
    }
}
