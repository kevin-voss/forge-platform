package forge.control.http

import io.ktor.http.HttpStatusCode
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import kotlinx.serialization.Serializable
import java.util.concurrent.atomic.AtomicBoolean

@Serializable
data class HealthResponse(val status: String)

/** Process readiness flag: false until the HTTP server has finished starting. */
class Readiness(initial: Boolean = false) {
    private val ready = AtomicBoolean(initial)

    fun markReady() {
        ready.set(true)
    }

    fun isReady(): Boolean = ready.get()
}

fun Route.healthRoutes(readiness: Readiness) {
    get("/health/live") {
        call.respond(HealthResponse(status = "live"))
    }
    get("/health/ready") {
        if (readiness.isReady()) {
            call.respond(HealthResponse(status = "ready"))
        } else {
            call.respond(HttpStatusCode.ServiceUnavailable, HealthResponse(status = "not_ready"))
        }
    }
}
