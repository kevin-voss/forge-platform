package forge.identity

import forge.identity.config.Config
import forge.identity.config.loadConfig
import forge.identity.db.Database
import forge.identity.health.AlwaysHealthyDb
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.health.healthRoutes
import forge.identity.logging.JsonLog
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
import io.ktor.server.request.httpMethod
import io.ktor.server.request.path
import io.ktor.server.routing.routing
import kotlinx.serialization.json.Json
import org.slf4j.event.Level
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread
import kotlin.system.exitProcess

fun main() {
    val cfg = try {
        loadConfig()
    } catch (e: IllegalArgumentException) {
        System.err.println("fatal: ${e.message}")
        exitProcess(1)
    }

    val log = JsonLog(cfg.serviceName, cfg.logLevel)
    val startedAtMs = AtomicLong(System.currentTimeMillis())
    val dbHolder = AtomicReference<Database?>(null)
    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000

    val dbProbe = DbProbe {
        val db = dbHolder.get()
        if (db == null) "database not connected" else db.check()
    }

    val connector = thread(name = "identity-db-connect", isDaemon = true) {
        connectAndMigrate(cfg, log, dbHolder)
    }

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeIdentityModule(cfg, readiness, dbProbe, log, startedAtMs) { cause ->
            log.warn("readiness db check failed", "error" to cause)
        }
        monitor.subscribe(ApplicationStarted) {
            readiness.markReady()
            log.info(
                "listening",
                "port" to cfg.port,
                "version" to cfg.serviceVersion,
                "env" to cfg.env,
            )
        }
    }

    Runtime.getRuntime().addShutdownHook(
        Thread {
            log.info("shutdown signal received", "signal" to "SIGTERM")
            connector.interrupt()
            server.stop(gracePeriodMillis = 1_000, timeoutMillis = graceMillis)
            dbHolder.getAndSet(null)?.close()
            log.info("shutdown complete")
            // HotSpot reports SIGTERM as 143 unless we halt(0) after a clean drain.
            Runtime.getRuntime().halt(0)
        },
    )

    log.info(
        "starting",
        "port" to cfg.port,
        "version" to cfg.serviceVersion,
        "env" to cfg.env,
        "log_level" to cfg.logLevel,
        "shutdown_grace_seconds" to cfg.shutdownGraceSeconds,
        "db_migrate_on_start" to cfg.database.migrateOnStart,
    )

    try {
        server.start(wait = true)
    } finally {
        connector.interrupt()
        dbHolder.getAndSet(null)?.close()
    }
}

internal fun connectAndMigrate(
    cfg: Config,
    log: JsonLog,
    dbHolder: AtomicReference<Database?>,
) {
    var delayMs = cfg.database.connectRetryInitialMs
    while (!Thread.currentThread().isInterrupted) {
        try {
            val db = Database.open(cfg.database, log)
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
                    val message = e.message ?: e.javaClass.simpleName
                    db.markFailure(message)
                    log.error("migration failed", "error" to message)
                    db.close()
                    // Migration failures refuse ready; retry so a fixed DB can recover.
                    Thread.sleep(delayMs)
                    delayMs = (delayMs * 2).coerceAtMost(cfg.database.connectRetryMaxMs)
                    continue
                }
            } else {
                db.markMigrationsApplied()
            }
            dbHolder.getAndSet(db)?.close()
            log.info("database ready")
            return
        } catch (e: InterruptedException) {
            Thread.currentThread().interrupt()
            return
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            log.warn(
                "database unavailable; retrying",
                "error" to message,
                "retry_ms" to delayMs,
            )
            try {
                Thread.sleep(delayMs)
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
                return
            }
            delayMs = (delayMs * 2).coerceAtMost(cfg.database.connectRetryMaxMs)
        }
    }
}

fun Application.forgeIdentityModule(
    cfg: Config,
    readiness: Readiness,
    dbProbe: DbProbe = AlwaysHealthyDb,
    log: JsonLog? = null,
    startedAtMs: AtomicLong = AtomicLong(System.currentTimeMillis()),
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

    if (log != null) {
        intercept(ApplicationCallPipeline.Monitoring) {
            val started = System.currentTimeMillis()
            val method = call.request.httpMethod.value
            val path = call.request.path()
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
                    )
                }
            }
        }
    }

    routing {
        healthRoutes(cfg, readiness, dbProbe, startedAtMs, onDbFailure)
    }
}
