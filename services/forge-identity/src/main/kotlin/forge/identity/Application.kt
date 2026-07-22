package forge.identity

import forge.identity.auth.authRoutes
import forge.identity.authz.AuthzService
import forge.identity.authz.PermissionMatrix
import forge.identity.authz.authzRoutes
import forge.identity.config.Config
import forge.identity.config.loadConfig
import forge.identity.db.Database
import forge.identity.health.AlwaysHealthyDb
import forge.identity.health.DbProbe
import forge.identity.health.Readiness
import forge.identity.health.healthRoutes
import forge.identity.http.ApiException
import forge.identity.http.apiError
import forge.identity.http.installRequestId
import forge.identity.http.toEnvelope
import forge.identity.logging.JsonLog
import forge.identity.org.orgRoutes
import forge.identity.project.projectRoutes
import forge.identity.user.userRoutes
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
    val tenancyHolder = AtomicReference<TenancyServices?>(null)
    val authHolder = AtomicReference<AuthServices?>(null)
    val authzHolder = AtomicReference<AuthzService?>(null)
    val readiness = Readiness()
    val graceMillis = cfg.shutdownGraceSeconds.toLong().coerceAtLeast(1) * 1_000

    val dbProbe = DbProbe {
        val db = dbHolder.get()
        if (db == null) "database not connected" else db.check()
    }

    val connector = thread(name = "identity-db-connect", isDaemon = true) {
        connectAndMigrate(cfg, log, dbHolder, tenancyHolder, authHolder, authzHolder)
    }

    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        forgeIdentityModule(
            cfg = cfg,
            readiness = readiness,
            dbProbe = dbProbe,
            log = log,
            startedAtMs = startedAtMs,
            tenancyRef = tenancyHolder,
            authRef = authHolder,
            authzRef = authzHolder,
            onDbFailure = { cause ->
                log.warn("readiness db check failed", "error" to cause)
            },
        )
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
            authzHolder.set(null)
            authHolder.set(null)
            tenancyHolder.set(null)
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
        authzHolder.set(null)
        authHolder.set(null)
        tenancyHolder.set(null)
        dbHolder.getAndSet(null)?.close()
    }
}

internal fun connectAndMigrate(
    cfg: Config,
    log: JsonLog,
    dbHolder: AtomicReference<Database?>,
    tenancyHolder: AtomicReference<TenancyServices?>,
    authHolder: AtomicReference<AuthServices?>,
    authzHolder: AtomicReference<AuthzService?>,
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
            val tenancy = TenancyServices.from(db, log)
            val auth = AuthServices.from(db, tenancy, cfg.auth, log)
            val authz = AuthzService.create(
                projects = tenancy.projects,
                orgs = tenancy.orgs,
                matrix = PermissionMatrix.default(cfg.authzMatrixVersion),
                log = log,
            )
            maybeSeedAdmin(cfg, tenancy, log)
            dbHolder.getAndSet(db)?.close()
            tenancyHolder.set(tenancy)
            authHolder.set(auth)
            authzHolder.set(authz)
            log.info(
                "database ready",
                "authz_matrix_version" to cfg.authzMatrixVersion,
            )
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

internal fun maybeSeedAdmin(cfg: Config, tenancy: TenancyServices, log: JsonLog) {
    val email = cfg.seedAdminEmail ?: return
    val existing = tenancy.users.findByEmail(email)
    if (existing != null) {
        log.info("seed admin already present", "user_id" to existing.id)
        return
    }
    val user = tenancy.users.create(email = email, displayName = "Admin")
    val org = tenancy.orgs.create(name = "Platform")
    tenancy.orgs.addMember(org.id, user.id, "organization-owner")
    log.info(
        "seed admin created",
        "user_id" to user.id,
        "org_id" to org.id,
        "email_domain" to email.substringAfter('@', "unknown"),
    )
}

fun Application.forgeIdentityModule(
    cfg: Config,
    readiness: Readiness,
    dbProbe: DbProbe = AlwaysHealthyDb,
    log: JsonLog? = null,
    startedAtMs: AtomicLong = AtomicLong(System.currentTimeMillis()),
    tenancy: TenancyServices? = null,
    tenancyRef: AtomicReference<TenancyServices?>? = null,
    auth: AuthServices? = null,
    authRef: AtomicReference<AuthServices?>? = null,
    authz: AuthzService? = null,
    authzRef: AtomicReference<AuthzService?>? = null,
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

    fun resolveTenancy(): TenancyServices =
        tenancy
            ?: tenancyRef?.get()
            ?: throw ApiException.ServiceUnavailable("identity stores not ready")

    fun resolveAuth(): AuthServices =
        auth
            ?: authRef?.get()
            ?: throw ApiException.ServiceUnavailable("identity auth not ready")

    fun resolveAuthz(): AuthzService =
        authz
            ?: authzRef?.get()
            ?: run {
                // Tests may wire tenancy without a prebuilt authz service.
                val t = tenancy ?: tenancyRef?.get()
                    ?: throw ApiException.ServiceUnavailable("identity authz not ready")
                AuthzService.create(
                    projects = t.projects,
                    orgs = t.orgs,
                    matrix = PermissionMatrix.default(cfg.authzMatrixVersion),
                    log = log,
                ).also { authzRef?.set(it) }
            }

    routing {
        healthRoutes(cfg, readiness, dbProbe, startedAtMs, onDbFailure)
        userRoutes { resolveTenancy().users }
        orgRoutes { resolveTenancy().orgs }
        projectRoutes { resolveTenancy().projects }
        authRoutes { resolveAuth().auth }
        authzRoutes { resolveAuthz() }
    }
}
