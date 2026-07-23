package admin

import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.Application
import io.ktor.server.application.install
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.get
import io.ktor.server.routing.put
import io.ktor.server.routing.routing
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.max

@Serializable
data class HealthResponse(val status: String)

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

    routing {
        get("/health/live") {
            call.respond(HealthResponse(status = "ok"))
        }
        get("/health/ready") {
            call.respond(HealthResponse(status = "ok"))
        }
        get("/") {
            val uptime = max(0.0, (System.currentTimeMillis() - startedAtMs.get()) / 1000.0)
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
            call.respond(adminConfig.get())
        }
        put("/admin/config") {
            val updated = call.receive<AdminConfig>()
            adminConfig.set(updated)
            call.respond(updated)
        }
    }
}
