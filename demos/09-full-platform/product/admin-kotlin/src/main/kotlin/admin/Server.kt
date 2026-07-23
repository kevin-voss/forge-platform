package admin

import io.ktor.http.HttpStatusCode
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationCallPipeline
import io.ktor.server.application.call
import io.ktor.server.application.install
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation
import io.ktor.server.request.httpMethod
import io.ktor.server.request.path
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.get
import io.ktor.server.routing.put
import io.ktor.server.routing.routing
import io.ktor.util.AttributeKey
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.max

@Serializable
data class HealthResponse(
    val status: String,
    val error: String? = null,
)

@Serializable
data class IdentityResponse(
    val service: String,
    val language: String,
    val status: String,
    val version: String? = null,
    @SerialName("uptime_seconds")
    val uptimeSeconds: Double? = null,
)

@Serializable
data class AdminConfig(
    @SerialName("notify_enabled")
    val notifyEnabled: Boolean = true,
    @SerialName("default_severity")
    val defaultSeverity: String = "medium",
    @SerialName("retention_days")
    val retentionDays: Int = 30,
)

private val statusAttr = AttributeKey<Int>("responseStatus")

fun Application.configureContractRoutes(
    cfg: Config,
    startedAtMs: AtomicLong = AtomicLong(System.currentTimeMillis()),
    adminConfig: AtomicReference<AdminConfig> = AtomicReference(AdminConfig()),
) {
    install(ContentNegotiation) {
        json(
            Json {
                encodeDefaults = true
                explicitNulls = false
            },
        )
    }

    intercept(ApplicationCallPipeline.Monitoring) {
        try {
            proceed()
        } finally {
            val path = call.request.path()
            if (path != "/health/live" && path != "/health/ready") {
                val status = call.attributes.getOrNull(statusAttr)
                    ?: call.response.status()?.value
                    ?: 200
                OtlpHttp.exportSpan(
                    serviceName = cfg.serviceName,
                    spanName = "HTTP ${call.request.httpMethod.value}",
                    traceparent = call.request.headers["traceparent"],
                    statusCode = status,
                    path = path,
                )
            }
        }
    }

    routing {
        get("/health/live") {
            call.attributes.put(statusAttr, 200)
            call.respond(HealthResponse(status = "ok"))
        }
        get("/health/ready") {
            if (cfg.capstoneBreak) {
                call.attributes.put(statusAttr, 503)
                call.respond(HttpStatusCode.ServiceUnavailable, HealthResponse(status = "not_ready", error = "capstone_break"))
            } else {
                call.attributes.put(statusAttr, 200)
                call.respond(HealthResponse(status = "ok"))
            }
        }
        get("/") {
            val uptime = max(0.0, (System.currentTimeMillis() - startedAtMs.get()) / 1000.0)
            call.attributes.put(statusAttr, 200)
            call.respond(
                IdentityResponse(
                    service = cfg.serviceName,
                    language = "kotlin",
                    status = "running",
                    version = cfg.serviceVersion,
                    uptimeSeconds = uptime,
                ),
            )
        }
        get("/admin/config") {
            call.attributes.put(statusAttr, 200)
            call.respond(adminConfig.get())
        }
        put("/admin/config") {
            val updated = call.receive<AdminConfig>()
            adminConfig.set(updated)
            call.attributes.put(statusAttr, 200)
            call.respond(updated)
        }
    }
}
