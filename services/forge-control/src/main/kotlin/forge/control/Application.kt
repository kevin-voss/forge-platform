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
import forge.control.http.environmentRoutes
import forge.control.http.healthRoutes
import forge.control.http.projectRoutes
import forge.control.http.serviceRoutes
import forge.control.http.toEnvelope
import forge.control.logging.JsonLog
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.service.ApplicationService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
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
    val auditRepo = JdbcAuditRepository(db.dataSource)
    val relationships = RelationshipValidator(projectRepo, applicationRepo)
    val services = ControlServices(
        projects = ProjectService(projectRepo, auditRepo, actor = actor),
        environments = EnvironmentService(projectRepo, environmentRepo, auditRepo, actor = actor),
        applications = ApplicationService(applicationRepo, relationships, auditRepo, actor = actor),
        services = ServiceService(serviceRepo, relationships, auditRepo, actor = actor),
    )

    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000
    val dbProbe = DbProbe { db.check() }

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeControlModule(cfg, readiness, dbProbe, services, log) { cause ->
            log.error("readiness db check failed", "error" to cause)
        }
        monitor.subscribe(ApplicationStarted) {
            readiness.markReady()
            log.info(
                "listening",
                "port" to cfg.port,
                "version" to cfg.serviceVersion,
                "env" to cfg.env,
                "auth_mode" to cfg.authMode,
            )
        }
    }

    Runtime.getRuntime().addShutdownHook(
        Thread {
            log.info("shutdown signal received", "signal" to "SIGTERM")
            server.stop(gracePeriodMillis = 1_000, timeoutMillis = graceMillis)
            db.close()
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
        "shutdown_grace_seconds" to cfg.shutdownGraceSeconds,
        "db_schema" to cfg.database.schema,
        "db_migrate_on_start" to cfg.database.migrateOnStart,
    )

    try {
        server.start(wait = true)
    } finally {
        db.close()
    }
}

fun Application.forgeControlModule(
    cfg: AppConfig,
    readiness: Readiness,
    dbProbe: DbProbe = AlwaysHealthyDb,
    services: ControlServices? = null,
    log: JsonLog? = null,
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
            val requestId = call.request.headers["X-Request-Id"] ?: "-"
            try {
                proceed()
            } finally {
                if (!path.startsWith("/health")) {
                    val status = call.response.status()?.value ?: 0
                    val durationMs = System.currentTimeMillis() - started
                    log.info(
                        "request",
                        "method" to method,
                        "path" to path,
                        "status" to status,
                        "duration_ms" to durationMs,
                        "request_id" to requestId,
                    )
                }
            }
        }
    }

    // cfg reserved for future auth middleware / request context (FORGE_AUTH_MODE).
    @Suppress("UNUSED_VARIABLE")
    val authMode = cfg.authMode

    routing {
        healthRoutes(readiness, dbProbe, onDbFailure)
        if (services != null) {
            projectRoutes(services.projects)
            environmentRoutes(services.environments)
            applicationRoutes(services.applications)
            serviceRoutes(services.services)
        }
    }
}
