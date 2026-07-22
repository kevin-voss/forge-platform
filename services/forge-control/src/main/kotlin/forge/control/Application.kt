package forge.control

import forge.control.config.AppConfig
import forge.control.config.loadAppConfig
import forge.control.db.Db
import forge.control.http.AlwaysHealthyDb
import forge.control.http.DbProbe
import forge.control.http.HealthResponse
import forge.control.http.Readiness
import forge.control.http.healthRoutes
import forge.control.logging.JsonLog
import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationStarted
import io.ktor.server.application.install
import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty
import io.ktor.server.plugins.calllogging.CallLogging
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation
import io.ktor.server.plugins.statuspages.StatusPages
import io.ktor.server.request.path
import io.ktor.server.response.respond
import io.ktor.server.routing.routing
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

    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000
    val dbProbe = DbProbe { db.check() }

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeControlModule(cfg, readiness, dbProbe) { cause ->
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
    onDbFailure: (String) -> Unit = {},
) {
    install(ContentNegotiation) {
        json(
            Json {
                encodeDefaults = true
                explicitNulls = false
            },
        )
    }

    install(CallLogging) {
        level = Level.INFO
        filter { call -> !call.request.path().startsWith("/health") }
    }

    install(StatusPages) {
        exception<Throwable> { call, cause ->
            call.respond(
                HttpStatusCode.InternalServerError,
                HealthResponse(status = "error"),
            )
            call.application.environment.log.error("unhandled error: ${cause.message}", cause)
        }
        status(HttpStatusCode.NotFound) { call, _ ->
            call.respond(HttpStatusCode.NotFound, HealthResponse(status = "not_found"))
        }
    }

    // cfg reserved for future auth middleware / request context (FORGE_AUTH_MODE).
    @Suppress("UNUSED_VARIABLE")
    val authMode = cfg.authMode

    routing {
        healthRoutes(readiness, dbProbe, onDbFailure)
    }
}
