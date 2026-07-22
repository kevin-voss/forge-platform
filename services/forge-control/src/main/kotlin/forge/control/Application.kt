package forge.control

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
import forge.control.reconcile.HttpGatewayClient
import forge.control.reconcile.HttpRuntimeClient
import forge.control.reconcile.JdbcDeploymentHistory
import forge.control.reconcile.JdbcLastHealthyStore
import forge.control.reconcile.JdbcReconcileStatusStore
import forge.control.reconcile.JdbcTransitionRecorder
import forge.control.reconcile.ReadinessGate
import forge.control.reconcile.ReconciliationController
import forge.control.reconcile.RepositoryDeploymentStore
import forge.control.reconcile.StartupRecovery
import forge.control.reconcile.TrafficShifter
import forge.control.scheduler.JdbcPlacementStore
import forge.control.scheduler.PlacementService
import forge.control.scheduler.SingleNodeScheduler
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

    // FORGE_AUTH_MODE=dev: attribute creates to synthetic actor until Identity 09.06.
    val actor = "dev"
    val projectRepo = JdbcProjectRepository(db.dataSource)
    val environmentRepo = JdbcEnvironmentRepository(db.dataSource)
    val applicationRepo = JdbcApplicationRepository(db.dataSource)
    val serviceRepo = JdbcServiceRepository(db.dataSource)
    val deploymentRepo = JdbcDeploymentRepository(db.dataSource)
    val auditRepo = JdbcAuditRepository(db.dataSource)
    val relationships = RelationshipValidator(projectRepo, applicationRepo)
    val deploymentStore = RepositoryDeploymentStore(
        deploymentRepo,
        serviceRepo,
        rolloutBatchSizeOverride = cfg.rolloutBatchSizeOverride,
        rolloutTimeoutOverride = cfg.rolloutTimeoutOverride,
    )
    val runtimeClient = HttpRuntimeClient(cfg.runtimeUrl)
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
    val placementStore = JdbcPlacementStore(db.dataSource)
    val placementScheduler = SingleNodeScheduler(
        if (cfg.schedulerEnabled) cfg.schedulerLocalNodeId else null,
    )
    val placementService = PlacementService(
        scheduler = placementScheduler,
        store = placementStore,
        log = log,
        telemetry = telemetry,
    )
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
    )

    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000
    val dbProbe = DbProbe { db.check() }

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeControlModule(cfg, readiness, dbProbe, services, log, telemetry) { cause ->
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
            )
        }
    }

    Runtime.getRuntime().addShutdownHook(
        Thread {
            log.info("shutdown signal received", "signal" to "SIGTERM")
            reconcileController.stop()
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

    if (log != null) {
        intercept(ApplicationCallPipeline.Monitoring) {
            val started = System.currentTimeMillis()
            val method = call.request.httpMethod.value
            val path = call.request.path()
            val span = telemetry.startSpan("HTTP $method")
            span.setAttribute("http.request.method", method)
            span.setAttribute("url.path", path)
            val scope = span.makeCurrent()
            try {
                proceed()
            } catch (error: Throwable) {
                span.recordException(error)
                span.setStatus(io.opentelemetry.api.trace.StatusCode.ERROR)
                throw error
            } finally {
                val status = call.response.status()?.value ?: 0
                span.setAttribute("http.response.status_code", status.toLong())
                if (status >= 500) span.setStatus(io.opentelemetry.api.trace.StatusCode.ERROR)
                if (!path.startsWith("/health")) {
                    val durationMs = System.currentTimeMillis() - started
                    telemetry.recordRequest(status, durationMs)
                    log.info(
                        "request",
                        "method" to method,
                        "path" to path,
                        "status" to status,
                        "duration_ms" to durationMs,
                    )
                }
                scope.close()
                span.end()
            }
        }
    }

    // cfg reserved for future auth middleware / request context (FORGE_AUTH_MODE).
    @Suppress("UNUSED_VARIABLE")
    val authMode = cfg.authMode

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
        }
    }
}
